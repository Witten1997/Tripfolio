package pgcore

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/write"
)

// SyncWriteOptions opts into epoch validation, admission and immutable receipt facts.
// Ordinary REST writes continue to use Run without these options.
type SyncWriteOptions struct{ Epoch uuid.UUID }

// admitNew runs only after successful receipt replay has been ruled out.
func (o SyncWriteOptions) admitNew(policy WebPolicy) error {
	if !policy.V2Enabled() || policy.Epoch != o.Epoch {
		return apperr.Conflicted("SYNC_NOT_READY", "账号尚未启用同步写入，请重新建立基线并完成启用")
	}
	return nil
}

func (o SyncWriteOptions) authorize(ctx context.Context, scope *TxScope) error {
	a, ok := actor.FromContext(ctx)
	if !ok || a.AccountID != scope.AccountID || a.SessionID == uuid.Nil || (a.ClientKind != actor.ClientHarmony && a.ClientKind != actor.ClientAndroid) {
		return apperr.Unauthorized("SESSION_EXPIRED", "请重新登录")
	}
	var epoch uuid.UUID
	if err := scope.Tx.QueryRow(ctx, `SELECT sync_epoch FROM account_sync_state WHERE account_id=$1`, scope.AccountID).Scan(&epoch); err != nil {
		return apperr.Internal(err)
	}
	if epoch != o.Epoch {
		return apperr.Conflicted("SYNC_EPOCH_MISMATCH", "请重新建立账号基线")
	}
	return nil
}

func (w *Writer) RunSync(ctx context.Context, req write.Request, options SyncWriteOptions, fn func(context.Context, *TxScope) error, reload func(context.Context, *TxScope) (any, error)) (write.Result, error) {
	return w.run(ctx, req, &options, fn, reload)
}

// RecordMembersRevision captures the complete effective member set under the account lock.
func (s *TxScope) RecordMembersRevision(ctx context.Context, epoch, tripID uuid.UUID) error {
	r, err := s.MembersRevision(ctx, epoch, tripID)
	if err != nil {
		return err
	}
	s.revisions = append(s.revisions, r)
	return nil
}

func (s *TxScope) MembersRevision(ctx context.Context, epoch, tripID uuid.UUID) (write.ScopeRevision, error) {
	return s.CollectionRevision(ctx, epoch, "members", tripID)
}
func (s *TxScope) RecordCollectionRevision(ctx context.Context, epoch uuid.UUID, kind string, tripID uuid.UUID) error {
	revision, err := s.CollectionRevision(ctx, epoch, kind, tripID)
	if err != nil {
		return err
	}
	s.revisions = append(s.revisions, revision)
	return nil
}
func (s *TxScope) CollectionRevision(ctx context.Context, epoch uuid.UUID, kind string, tripID uuid.UUID) (write.ScopeRevision, error) {
	var out write.ScopeRevision
	table := ""
	switch kind {
	case "categories":
		if tripID != s.AccountID {
			return out, apperr.Unprocessable("INVALID_REFERENCE", "分类集合不属于本账号")
		}
		table = "expense_categories"
	case "members":
		table = "trip_members"
	case "packing_order":
		table = "packing_items"
	case "todo_order":
		table = "todo_items"
	default:
		return out, apperr.Unprocessable("INVALID_REFERENCE", "不支持的集合")
	}
	query := `SELECT id,version FROM ` + table + ` WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id`
	args := []any{s.AccountID, tripID}
	if kind == "categories" {
		query = `SELECT id,version FROM expense_categories WHERE account_id=$1 AND deleted_at IS NULL ORDER BY id`
		args = []any{s.AccountID}
	}
	rows, err := s.Tx.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n%s\n", epoch, kind, tripID)
	for rows.Next() {
		var id uuid.UUID
		var version int64
		if err := rows.Scan(&id, &version); err != nil {
			return out, err
		}
		fmt.Fprintf(h, "%s:%d\n", id, version)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	return write.ScopeRevision{Kind: kind, ScopeID: tripID.String(), Revision: fmt.Sprintf("sha256:%x", h.Sum(nil))}, nil
}
