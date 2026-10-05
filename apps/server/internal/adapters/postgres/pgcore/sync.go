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

// SyncWriteOptions opts into epoch validation and immutable receipt facts.
// Ordinary REST writes continue to use Run without these options.
type SyncWriteOptions struct{ Epoch uuid.UUID }

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
	var out write.ScopeRevision
	rows, err := s.Tx.Query(ctx, `SELECT id,version FROM trip_members WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id`, s.AccountID, tripID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	h := sha256.New()
	fmt.Fprintf(h, "%s\nmembers\n%s\n", epoch, tripID)
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
	return write.ScopeRevision{Kind: "members", ScopeID: tripID.String(), Revision: fmt.Sprintf("sha256:%x", h.Sum(nil))}, nil
}
