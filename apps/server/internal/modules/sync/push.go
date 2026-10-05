package sync

import (
	"context"
	"strconv"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
)

func (s *Service) WithPush(repo PushRepository) *Service { s.pushes = repo; return s }

func (s *Service) Push(ctx context.Context, a actor.Actor, protocol string, in PushInput) (PushOutput, error) {
	var out PushOutput
	if err := nativeActor(a); err != nil {
		return out, err
	}
	if protocol != "2" {
		return out, apperr.New(426, "SYNC_PROTOCOL_UNSUPPORTED", "需要同步协议版本2")
	}
	if err := validateBatch(in); err != nil {
		return out, err
	}
	status, err := s.Status(ctx, a)
	if err != nil {
		return out, err
	}
	if status.SyncEpoch != in.SyncEpoch {
		return out, apperr.Conflicted("SYNC_EPOCH_MISMATCH", "请重新建立账号基线")
	}
	if s.pushes == nil {
		return out, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "推送尚未装配")
	}
	out = PushOutput{SyncEpoch: in.SyncEpoch, Results: make([]PushResult, 0, len(in.Operations))}
	blocked := map[uuid.UUID]bool{}
	for _, op := range in.Operations {
		if err := ctx.Err(); err != nil {
			return PushOutput{}, apperr.Dependency(err)
		}
		prepared, err := prepare(op)
		if err != nil {
			st, authErr := s.Status(ctx, a)
			if authErr != nil {
				return PushOutput{}, authErr
			}
			if st.SyncEpoch != in.SyncEpoch {
				return PushOutput{}, apperr.Conflicted("SYNC_EPOCH_MISMATCH", "请重新建立账号基线")
			}
		}
		item := PushResult{OperationID: op.OperationID}
		if err == nil {
			prepared.BlockedDependencies = blocked
			result, execErr := s.pushes.Execute(actor.WithActor(ctx, a), a, in.SyncEpoch, prepared)
			err = execErr
			if err == nil {
				if result.Sync == nil {
					return PushOutput{}, apperr.Internal(nil)
				}
				item.Status = "applied"
				if result.Replayed {
					item.Status = "replayed"
				}
				item.Result = &OperationResult{References: result.Sync.References, ScopeRevisions: result.Sync.ScopeRevisions, Warnings: result.Warnings, Data: result.Data}
				if result.Sync.CommitSeq != nil {
					token, e := s.commitCursor(a.AccountID, result.Sync.Epoch, op.OperationID, *result.Sync.CommitSeq)
					if e != nil {
						return PushOutput{}, apperr.Internal(e)
					}
					item.Result.CommitCursor = &token
				}
			}
		}
		if err != nil {
			e, ok := apperr.As(err)
			if !ok {
				e = apperr.Internal(err)
			}
			switch e.Code {
			case "SESSION_EXPIRED", "ACCOUNT_BANNED", "ACCOUNT_DELETING", "ACCOUNT_DISABLED", "SYNC_EPOCH_MISMATCH":
				return PushOutput{}, e
			}
			item.Status = "rejected"
			retryable := false
			switch {
			case e.Status == 412:
				item.Status = "conflict"
			case e.Status == 424:
				item.Status = "dependency_failed"
				retryable = e.Code == "DEPENDENCY_NOT_APPLIED"
			case e.Status >= 500:
				item.Status = "failed"
				retryable = true
			}
			item.Error = &OperationError{Code: e.Code, HTTPStatus: e.Status, Retryable: retryable, Detail: e.Detail}
			if c := e.Conflict; c != nil {
				item.Error.Conflict = map[string]any{"entity_type": c.EntityType, "entity_id": c.EntityID, "expected_version": strconv.FormatInt(c.ExpectedVersion, 10), "current_version": strconv.FormatInt(c.CurrentVersion, 10), "conflicting_fields": c.ConflictingFields, "current": c.Current}
			}
			blocked[op.OperationID] = !retryable
		}
		out.Results = append(out.Results, item)
	}
	return out, nil
}
