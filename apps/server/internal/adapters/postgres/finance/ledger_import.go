package financepg

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/write"

	"github.com/google/uuid"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/modules/travel/member"
)

func importCategories(ctx context.Context, q *dbgen.Queries, accountID uuid.UUID) ([]finance.CategoryResource, error) {
	rows, err := q.ListExpenseCategories(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]finance.CategoryResource, 0, len(rows))
	for _, row := range rows {
		out = append(out, toResource(row))
	}
	return out, nil
}

func (r *ledgerRepo) ImportCategories(ctx context.Context, accountID uuid.UUID) ([]finance.CategoryResource, error) {
	return importCategories(ctx, r.scope.Queries, accountID)
}

func (r *LedgerReader) ImportCategories(ctx context.Context, accountID uuid.UUID) ([]finance.CategoryResource, error) {
	return importCategories(ctx, r.q, accountID)
}

func (r *LedgerReader) ActiveMembers(ctx context.Context, accountID, tripID uuid.UUID) ([]member.Resource, error) {
	return travelpg.ListActiveMembers(ctx, r.q, accountID, tripID)
}

// 两次集合写入复用当前账号写事务，避免逐条往返远端数据库。
func (r *ledgerRepo) InsertImported(ctx context.Context, accountID, tripID uuid.UUID, entries []finance.LedgerResource) error {
	ids := make([]uuid.UUID, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	var used bool
	if err := r.scope.Tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ledger_entries WHERE id = ANY($2::uuid[]))
		OR EXISTS (SELECT 1 FROM entity_tombstones WHERE account_id = $1 AND entity_type = 'ledger_entry' AND entity_id = ANY($2::uuid[]))`, accountID, ids).Scan(&used); err != nil {
		return err
	}
	if used {
		return apperr.Conflicted("ID_ALREADY_USED", "该批次已导入，请刷新账单列表")
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	_, err = r.scope.Tx.Exec(ctx, `INSERT INTO ledger_entries
		(id, account_id, trip_id, kind, amount, split_count, personal_amount, payer_member_id, split_mode, category_id, occurred_on, notes, created_at, updated_at)
		SELECT e.id, $1, $2, 'expense', e.amount, e.split_count, e.personal_amount, e.payer_member_id, e.split_mode, e.category_id, e.occurred_on, e.notes, e.created_at, e.created_at
		FROM jsonb_to_recordset($3::jsonb) AS e(id uuid, amount numeric, split_count integer, personal_amount numeric, payer_member_id uuid, split_mode text, category_id uuid, occurred_on date, notes text, created_at timestamptz)`, accountID, tripID, data)
	if err != nil {
		return err
	}
	_, err = r.scope.Tx.Exec(ctx, `INSERT INTO ledger_entry_splits (account_id, trip_id, ledger_entry_id, member_id, amount, sort_order)
		SELECT $1, $2, (e->>'id')::uuid, (s.value->>'member_id')::uuid, (s.value->>'amount')::numeric, (s.ordinality - 1)::integer
		FROM jsonb_array_elements($3::jsonb) AS e
		CROSS JOIN LATERAL jsonb_array_elements(e->'splits') WITH ORDINALITY AS s(value, ordinality)`, accountID, tripID, data)
	return err
}

func (r *LedgerReader) ReadImportSnapshot(ctx context.Context, accountID, tripID uuid.UUID) (finance.ImportSnapshot, error) {
	var out finance.ImportSnapshot
	if r.pool == nil {
		return out, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "集合基线读取暂不可用")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, apperr.Internal(err)
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	info, found, err := ledgerTripInfo(ctx, q, accountID, tripID)
	if err != nil {
		return out, apperr.Internal(err)
	}
	if !found {
		return out, apperr.NotFound()
	}
	if info.DeletedAt != nil {
		return out, apperr.Gone("TRIP_DELETED", "旅行已在回收站中")
	}
	var epoch uuid.UUID
	err = tx.QueryRow(ctx, `SELECT sync_epoch FROM account_sync_state WHERE account_id=$1`, accountID).Scan(&epoch)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && epoch == uuid.Nil) {
		return out, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "集合基线暂不可用")
	}
	if err != nil {
		return out, apperr.Internal(err)
	}
	out.Trip = info
	out.Categories, err = importCategories(ctx, q, accountID)
	if err != nil {
		return out, apperr.Internal(err)
	}
	out.Members, err = travelpg.ListActiveMembers(ctx, q, accountID, tripID)
	if err != nil {
		return out, apperr.Internal(err)
	}
	scope := &pgcore.TxScope{Tx: tx, Queries: q, AccountID: accountID}
	revision, err := scope.MembersRevision(ctx, epoch, tripID)
	if err != nil {
		return out, err
	}
	out.ScopeRevisions = []write.ScopeRevision{revision}
	if err := tx.Commit(ctx); err != nil {
		return finance.ImportSnapshot{}, apperr.Internal(err)
	}
	return out, nil
}
