package travelpg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/member"
)

// 本文件实现 member 模块的 PostgreSQL 适配（数据库设计表 25）；share_percent NUMERIC(5,2) 转回去尾随零的字符串。

// ToMemberResource 把成员行转成规范资源；finance 适配器读取参与人时复用。
func ToMemberResource(row dbgen.TripMember) (member.Resource, error) {
	percent, err := money.ParseDecimal(row.SharePercent)
	if err != nil {
		return member.Resource{}, fmt.Errorf("成员 %s 的百分比 %q 无法解析: %w", row.ID, row.SharePercent, err)
	}
	return member.Resource{
		ID: row.ID, TripID: row.TripID, Name: row.Name, SharePercent: percent.Format(0), SortOrder: row.SortOrder, IsSelf: row.IsSelf,
		Version: types.Version(row.Version), CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt), DeletedAt: pgcore.UTCPtr(row.DeletedAt),
	}, nil
}

func toMemberResources(rows []dbgen.TripMember) ([]member.Resource, error) {
	out := make([]member.Resource, 0, len(rows))
	for _, row := range rows {
		r, err := ToMemberResource(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ListActiveMembers 返回旅行的全部有效成员；finance 适配器校验付款人与参与人时复用。
func ListActiveMembers(ctx context.Context, q *dbgen.Queries, accountID, tripID uuid.UUID) ([]member.Resource, error) {
	rows, err := q.ListTripMembers(ctx, dbgen.ListTripMembersParams{AccountID: accountID, TripID: tripID})
	if err != nil {
		return nil, err
	}
	return toMemberResources(rows)
}

func insertMember(ctx context.Context, q *dbgen.Queries, accountID uuid.UUID, m member.Resource) (member.Resource, error) {
	row, err := q.InsertTripMember(ctx, dbgen.InsertTripMemberParams{
		ID: m.ID, AccountID: accountID, TripID: m.TripID, Name: m.Name, SharePercent: m.SharePercent, SortOrder: m.SortOrder, IsSelf: m.IsSelf, CreatedAt: m.CreatedAt,
	})
	if err != nil {
		return member.Resource{}, err
	}
	return ToMemberResource(row)
}

func memberTripInfo(ctx context.Context, q *dbgen.Queries, accountID, tripID uuid.UUID) (member.TripInfo, bool, error) {
	info, err := q.GetTripContentInfo(ctx, dbgen.GetTripContentInfoParams{AccountID: accountID, ID: tripID})
	if errors.Is(err, pgx.ErrNoRows) {
		return member.TripInfo{}, false, nil
	}
	if err != nil {
		return member.TripInfo{}, false, err
	}
	return member.TripInfo{DeletedAt: pgcore.UTCPtr(info.DeletedAt)}, true, nil
}

// memberRepo 绑定到一次写事务。
type memberRepo struct {
	scope *pgcore.TxScope
}

var _ member.Repo = (*memberRepo)(nil)

// NewMemberUnitOfWork 创建成员写事务入口。
func NewMemberUnitOfWork(writer *pgcore.Writer) write.UnitOfWork[member.Repo] {
	return pgcore.NewUnitOfWork(writer, func(scope *pgcore.TxScope) member.Repo {
		return &memberRepo{scope: scope}
	})
}

func (r *memberRepo) Trip(ctx context.Context, accountID, tripID uuid.UUID) (member.TripInfo, bool, error) {
	return memberTripInfo(ctx, r.scope.Queries, accountID, tripID)
}

func (r *memberRepo) ListForUpdate(ctx context.Context, accountID, tripID uuid.UUID) ([]member.Resource, error) {
	rows, err := r.scope.Queries.ListTripMembersForUpdate(ctx, dbgen.ListTripMembersForUpdateParams{AccountID: accountID, TripID: tripID})
	if err != nil {
		return nil, err
	}
	return toMemberResources(rows)
}

func (r *memberRepo) IDExists(ctx context.Context, id uuid.UUID) (bool, error) {
	exists, err := r.scope.Queries.TripMemberIDExists(ctx, dbgen.TripMemberIDExistsParams{ID: id, AccountID: r.scope.AccountID})
	return boolOf(exists), err
}

func (r *memberRepo) Insert(ctx context.Context, accountID uuid.UUID, m member.Resource) (member.Resource, error) {
	return insertMember(ctx, r.scope.Queries, accountID, m)
}

func (r *memberRepo) Update(ctx context.Context, accountID, tripID, id uuid.UUID, v member.Values, now time.Time) (member.Resource, error) {
	row, err := r.scope.Queries.UpdateTripMember(ctx, dbgen.UpdateTripMemberParams{
		AccountID: accountID, TripID: tripID, ID: id, Name: v.Name, SharePercent: v.SharePercent, SortOrder: v.SortOrder, UpdatedAt: now,
	})
	if err != nil {
		return member.Resource{}, err
	}
	return ToMemberResource(row)
}

func (r *memberRepo) SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (member.Resource, error) {
	row, err := r.scope.Queries.SoftDeleteTripMember(ctx, dbgen.SoftDeleteTripMemberParams{AccountID: accountID, TripID: tripID, ID: id, DeletedAt: &now})
	if err != nil {
		return member.Resource{}, err
	}
	return ToMemberResource(row)
}

func (r *memberRepo) ReferenceCount(ctx context.Context, accountID, tripID, memberID uuid.UUID) (int64, error) {
	return r.scope.Queries.CountTripMemberReferences(ctx, dbgen.CountTripMemberReferencesParams{AccountID: accountID, TripID: tripID, MemberID: memberID})
}

// MemberReader 是事务外只读仓储。
type MemberReader struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

// NewMemberReader 创建只读仓储。
func NewMemberReader(pool *pgxpool.Pool) *MemberReader {
	return &MemberReader{pool: pool, q: dbgen.New(pool)}
}

var _ member.Reader = (*MemberReader)(nil)

// Trip 实现 member.Reader。
func (r *MemberReader) Trip(ctx context.Context, accountID, tripID uuid.UUID) (member.TripInfo, bool, error) {
	return memberTripInfo(ctx, r.q, accountID, tripID)
}

// List 实现 member.Reader。
func (r *MemberReader) List(ctx context.Context, accountID, tripID uuid.UUID) ([]member.Resource, error) {
	return ListActiveMembers(ctx, r.q, accountID, tripID)
}

func NewMemberRepository(scope *pgcore.TxScope) member.Repo { return &memberRepo{scope: scope} }

func (r *memberRepo) PrepareNames(ctx context.Context, owner, tripID uuid.UUID, final map[uuid.UUID]string) error {
	current, err := r.ListForUpdate(ctx, owner, tripID)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(final))
	for _, name := range final {
		names = append(names, name)
	}
	rows, err := r.scope.Tx.Query(ctx, `SELECT lower(btrim(name)) FROM trip_members WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL UNION SELECT lower(btrim(value)) FROM unnest($3::text[]) AS value`, owner, tripID, names)
	if err != nil {
		return err
	}
	reserved := map[string]bool{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		reserved[key] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	counter := uint64(0)
	for _, m := range current {
		name, keep := final[m.ID]
		if !keep || name == m.Name {
			continue
		}
		var temporary string
		for {
			temporary = fmt.Sprintf("~sync%d", counter)
			counter++
			if !reserved[temporary] {
				break
			}
		}
		reserved[temporary] = true
		// Temporary names are ASCII, <= 25 characters, and compared with the
		// database's exact lower(btrim(name)) keys above, never random guesses.
		if _, err = r.scope.Tx.Exec(ctx, `UPDATE trip_members SET name=$4 WHERE account_id=$1 AND trip_id=$2 AND id=$3 AND deleted_at IS NULL`, owner, tripID, m.ID, temporary); err != nil {
			return err
		}
	}
	return nil
}

func (r *MemberReader) ListWithBaseline(ctx context.Context, accountID, tripID uuid.UUID) (member.ListBaseline, error) {
	var out member.ListBaseline
	if r.pool == nil {
		return out, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "集合基线读取暂不可用")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, apperr.Internal(err)
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	info, found, err := memberTripInfo(ctx, q, accountID, tripID)
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
	members, err := ListActiveMembers(ctx, q, accountID, tripID)
	if err != nil {
		return out, apperr.Internal(err)
	}
	out.Members = members
	scope := &pgcore.TxScope{Tx: tx, Queries: q, AccountID: accountID}
	revision, err := scope.MembersRevision(ctx, epoch, tripID)
	if err != nil {
		return out, err
	}
	out.ScopeRevisions = []write.ScopeRevision{revision}
	if err := tx.Commit(ctx); err != nil {
		return member.ListBaseline{}, apperr.Internal(err)
	}
	return out, nil
}
