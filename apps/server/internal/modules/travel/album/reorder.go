package album

import (
	"context"
	"math"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/content"
)

// Reorder preserves the existing photo ordering fields. The sync caller checks
// the complete day's guard under this same account transaction.
func (s *Service) Reorder(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID, day types.Date, ids []uuid.UUID) (write.Result, error) {
	if err := content.Date("recorded_on", day); err != nil {
		return write.Result{}, err
	}
	if len(ids) > math.MaxInt32 {
		return write.Result{}, apperr.Validation(apperr.Field("ordered_ids", "INVALID", "照片数量超出排序范围"))
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			return write.Result{}, apperr.Validation(apperr.Field("ordered_ids", "INVALID", "排序ID必须有效且无重复"))
		}
		seen[id] = true
	}
	req := write.Request{AccountID: a.AccountID, OperationID: operationID, OperationType: "photo.reorder", Fingerprint: write.Fingerprint("photo.reorder", tripID.String()+"/"+string(day), nil, ids)}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if _, err := content.LoadTrip(ctx, repo, a.AccountID, tripID); err != nil {
			return err
		}
		rows, err := repo.ListForOrder(ctx, a.AccountID, tripID, day)
		if err != nil {
			return err
		}
		if len(rows) != len(ids) {
			return apperr.Validation(apperr.Field("ordered_ids", "INCOMPLETE", "必须包含该日期完整有效集合"))
		}
		byID := map[uuid.UUID]Resource{}
		for _, row := range rows {
			if !seen[row.ID] {
				return apperr.Validation(apperr.Field("ordered_ids", "INCOMPLETE", "必须包含该日期完整有效集合"))
			}
			byID[row.ID] = row
		}
		for i, id := range ids {
			row := byID[id]
			if row.SortOrder != int32(i) {
				row.SortOrder = int32(i)
				row.UpdatedAt = s.clock.Now()
				row, err = repo.Update(ctx, a.AccountID, row)
				if err != nil {
					return err
				}
				record(scope, row, write.ChangeUpsert, []string{"sort_order"})
			}
			scope.AddAffected(write.Ref(EntityType, row.ID, int64(row.Version)))
		}
		return nil
	}, nil)
}
