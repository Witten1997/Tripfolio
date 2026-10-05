package sync

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"strings"
	financepg "tripfolio/server/internal/adapters/postgres/finance"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/finance"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/modules/travel/member"
)

func isFinanceEntity(entity string) bool {
	return entity == "expense_category" || entity == "trip_member" || entity == "ledger_entry"
}

func executeFinance(ctx context.Context, scope *pgcore.TxScope, a actor.Actor, epoch uuid.UUID, op syncmodule.PreparedOperation, base int64, deps map[uuid.UUID]*write.SyncFacts) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(op.Payload, &fields); err != nil {
		return err
	}
	kind, scopeID := "members", a.AccountID
	if op.EntityType == "expense_category" {
		kind = "categories"
	} else {
		scopeID = *op.TripID
	}
	required := op.Type == "expense_category.reorder" || op.Type == "trip_member.replace" || op.Type == "ledger_entry.create"
	if op.Type == "ledger_entry.update" {
		for _, key := range []string{"amount", "currency_code", "payer_member_id", "split_mode", "participant_member_ids"} {
			if _, ok := fields[key]; ok {
				required = true
			}
		}
	}
	if required && len(op.Guards) == 0 {
		return apperr.New(428, "COLLECTION_BASE_REQUIRED", "本次修改需要集合版本")
	}
	guards, err := resolveCollectionGuards(op.Guards, deps)
	if err != nil {
		return err
	}
	if required || len(guards) > 0 {
		if err := scope.CheckCollections(ctx, guards, []collectionguard.Scope{{Kind: kind, ScopeID: scopeID.String()}}); err != nil {
			return err
		}
	}
	var id uuid.UUID
	if op.EntityID != nil {
		id = *op.EntityID
	}
	err = nil
	switch op.EntityType {
	case "expense_category":
		svc := finance.NewCategoryService(boundItemUOW[finance.CategoryRepo]{scope, financepg.NewCategoryRepository(scope)}, nil, transactionClock{scope.Now})
		switch op.Type {
		case "expense_category.create":
			var c finance.CreateCategoryCommand
			if err = json.Unmarshal(op.Payload, &c); err != nil {
				return err
			}
			c.ID = id
			_, err = svc.Create(ctx, a, op.OperationID, c)
		case "expense_category.update":
			var p finance.CategoryPatch
			if err = json.Unmarshal(op.Payload, &p); err != nil {
				return err
			}
			_, p.IconSet = fields["icon"]
			_, err = svc.Update(ctx, a, op.OperationID, id, base, p)
		case "expense_category.delete":
			_, err = svc.Delete(ctx, a, op.OperationID, id, base)
		case "expense_category.reorder":
			var p struct {
				IDs []uuid.UUID `json:"ordered_ids"`
			}
			if err = json.Unmarshal(op.Payload, &p); err != nil {
				return err
			}
			_, err = svc.Reorder(ctx, a, op.OperationID, p.IDs)
		}
	case "trip_member":
		svc := member.NewService(boundItemUOW[member.Repo]{scope, travelpg.NewMemberRepository(scope)}, nil, transactionClock{scope.Now})
		var c member.SaveCommand
		if err = json.Unmarshal(op.Payload, &c); err != nil {
			return err
		}
		_, err = svc.Save(ctx, a, op.OperationID, *op.TripID, c)
	case "ledger_entry":
		svc := finance.NewLedgerService(boundItemUOW[finance.LedgerRepo]{scope, financepg.NewLedgerRepository(scope)}, nil, nil, transactionClock{scope.Now})
		switch op.Type {
		case "ledger_entry.create":
			var p finance.LedgerPatch
			if err = json.Unmarshal(op.Payload, &p); err != nil {
				return err
			}
			var kind string
			if err = json.Unmarshal(fields["kind"], &kind); err != nil {
				return err
			}
			c := finance.CreateLedgerCommand{ID: id, Kind: kind, Amount: *p.Amount, CurrencyCode: p.CurrencyCode, CategoryID: *p.CategoryID, OccurredOn: p.OccurredOn, Notes: p.Notes, RefundedEntryID: p.RefundedEntryID, AttachmentAssetIDs: *p.AttachmentAssetIDs, PayerMemberID: p.PayerMemberID, SplitMode: p.SplitMode, ParticipantMemberIDs: *p.ParticipantMemberIDs}
			_, err = svc.Create(ctx, a, op.OperationID, *op.TripID, c)
		case "ledger_entry.update":
			var p finance.LedgerPatch
			if err = json.Unmarshal(op.Payload, &p); err != nil {
				return err
			}
			_, p.RefundedSet = fields["refunded_entry_id"]
			_, err = svc.Update(ctx, a, op.OperationID, *op.TripID, id, base, p)
		case "ledger_entry.delete":
			_, err = svc.Delete(ctx, a, op.OperationID, *op.TripID, id, base)
		}
	}
	if e, ok := apperr.As(err); ok && e.Code == "VERSION_CONFLICT" && e.Conflict != nil && e.Conflict.ExpectedVersion < e.Conflict.CurrentVersion && e.Conflict.ConflictingFields == nil && strings.HasSuffix(op.Type, ".update") {
		copy := *e
		copy.Code = "MERGE_HISTORY_UNAVAILABLE"
		return &copy
	}
	if err != nil {
		return err
	}
	if err := scope.RecordCollections(ctx, []collectionguard.Scope{{Kind: kind, ScopeID: scopeID.String()}}); err != nil {
		return err
	}
	return scope.RecordCollectionRevision(ctx, epoch, kind, scopeID)
}

func reloadFinance(ctx context.Context, scope *pgcore.TxScope, a actor.Actor, op syncmodule.Operation) (any, error) {
	if op.EntityType == "expense_category" {
		repo := financepg.NewCategoryRepository(scope)
		if op.EntityID == nil {
			return repo.ListForOrder(ctx, a.AccountID)
		}
		r, found, err := repo.Get(ctx, a.AccountID, *op.EntityID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, nil
		}
		return r, nil
	}
	trip, found, err := travelpg.NewTripRepository(scope).Get(ctx, a.AccountID, *op.TripID)
	if err != nil {
		return nil, err
	}
	if !found || trip.PurgeRequestedAt != nil {
		return nil, nil
	}
	if op.EntityType == "trip_member" {
		return travelpg.NewMemberRepository(scope).ListForUpdate(ctx, a.AccountID, *op.TripID)
	}
	r, found, err := financepg.NewLedgerRepository(scope).Get(ctx, a.AccountID, *op.TripID, *op.EntityID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return r, nil
}
