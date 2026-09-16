package itinerary

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
)

// ReorderItem 是重排中的一个项目及其基线版本。
type ReorderItem struct {
	ID          uuid.UUID `json:"id"`
	BaseVersion int64     `json:"base_version"`
}

// ReorderDay 是一天的完整行程集合，数组顺序即新的 sort_order。
type ReorderDay struct {
	Date  string        `json:"date"`
	Items []ReorderItem `json:"items"`
}

// ReorderCommand 是重排命令（接口设计 3.3 ItineraryReorder）。
type ReorderCommand struct {
	Days []ReorderDay `json:"days"`
}

// Reorder 重排受影响日期的行程，支持跨日移动；整体在一个事务中完成。
// 提交的集合必须与这些日期当前的全部有效项目一致，否则 409 ORDER_CHANGED；
// 个别基线版本不符返回 412 VERSION_CONFLICT，不做字段级合并。
func (s *Service) Reorder(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID, cmd ReorderCommand) (write.Result, error) {
	var fields []apperr.FieldError
	if len(cmd.Days) == 0 {
		return write.Result{}, apperr.Validation(apperr.Field("days", "REQUIRED", "至少提交一天"))
	}
	dates := make([]types.Date, len(cmd.Days))
	seenDates := make(map[types.Date]struct{}, len(cmd.Days))
	seenIDs := make(map[uuid.UUID]struct{})
	for i, day := range cmd.Days {
		d, ferr := parseDate(fmt.Sprintf("days[%d].date", i), day.Date)
		addField(&fields, ferr)
		dates[i] = d
		if d != "" {
			if _, dup := seenDates[d]; dup {
				fields = append(fields, apperr.Field(fmt.Sprintf("days[%d].date", i), "DUPLICATE", "同一日期只能出现一次"))
			}
			seenDates[d] = struct{}{}
		}
		for j, item := range day.Items {
			path := fmt.Sprintf("days[%d].items[%d]", i, j)
			if item.ID == uuid.Nil {
				fields = append(fields, apperr.Field(path+".id", "INVALID", "id 必填"))
				continue
			}
			if _, dup := seenIDs[item.ID]; dup {
				fields = append(fields, apperr.Field(path+".id", "DUPLICATE", "同一项目只能出现一次"))
			}
			seenIDs[item.ID] = struct{}{}
			if item.BaseVersion < 1 {
				fields = append(fields, apperr.Field(path+".base_version", "INVALID", "基线版本必须是正整数"))
			}
		}
	}
	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}

	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "itinerary.reorder",
		Fingerprint: write.Fingerprint("itinerary.reorder", tripID.String(), nil, cmd),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		info, err := loadTrip(ctx, repo, a.AccountID, tripID)
		if err != nil {
			return err
		}
		rows, err := repo.ListDaysForUpdate(ctx, a.AccountID, tripID, dates)
		if err != nil {
			return err
		}
		current := make(map[uuid.UUID]Resource, len(rows))
		for _, r := range rows {
			current[r.ID] = r
		}
		if len(seenIDs) != len(current) {
			return orderChanged()
		}
		for id := range seenIDs {
			if _, ok := current[id]; !ok {
				return orderChanged()
			}
		}
		// 版本按提交顺序校验，冲突时报告第一处不符。
		for _, day := range cmd.Days {
			for _, item := range day.Items {
				row := current[item.ID]
				if int64(row.Version) != item.BaseVersion {
					return apperr.VersionConflict(&apperr.Conflict{
						EntityType: EntityType, EntityID: item.ID, ExpectedVersion: item.BaseVersion,
						CurrentVersion: int64(row.Version), Current: row,
					})
				}
			}
		}
		now := s.clock.Now()
		for i, day := range cmd.Days {
			date := dates[i]
			for order, item := range day.Items {
				row := current[item.ID]
				if row.ScheduledOn == date && row.SortOrder == int32(order) {
					continue
				}
				moved, err := repo.Reposition(ctx, a.AccountID, tripID, item.ID, date, int32(order), now)
				if err != nil {
					return err
				}
				if outsideTripDates(date, info) {
					scope.Warn(write.WarnItineraryOutsideTripDates)
				}
				record(scope, moved, write.ChangeUpsert, PositionFields)
			}
		}
		return recalculateRoutes(ctx, scope, repo, a.AccountID, tripID, now)
	}, nil)
}

func orderChanged() *apperr.Error {
	return apperr.Conflicted(codeOrderChanged, "这些日期的行程已经变化，请刷新后重新排序")
}
