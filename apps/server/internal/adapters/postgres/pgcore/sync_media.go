package pgcore

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
)

func (s *TxScope) PhotoDayRevision(ctx context.Context, epoch, tripID uuid.UUID, day types.Date) (write.ScopeRevision, error) {
	if epoch == uuid.Nil || tripID == uuid.Nil {
		return write.ScopeRevision{}, apperr.Unprocessable("INVALID_REFERENCE", "照片日期范围无效")
	}
	if _, err := types.ParseDate(string(day)); err != nil {
		return write.ScopeRevision{}, apperr.Unprocessable("INVALID_REFERENCE", "照片日期范围无效")
	}
	var exists bool
	if err := s.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM trips WHERE account_id=$1 AND id=$2 AND deleted_at IS NULL AND purge_requested_at IS NULL)`, s.AccountID, tripID).Scan(&exists); err != nil {
		return write.ScopeRevision{}, err
	}
	if !exists {
		return write.ScopeRevision{}, apperr.NotFound()
	}
	scopeID := tripID.String() + "/" + string(day)
	rows, err := s.Tx.Query(ctx, `SELECT id,version FROM photos WHERE account_id=$1 AND trip_id=$2 AND recorded_on=$3 AND deleted_at IS NULL ORDER BY id`, s.AccountID, tripID, day.Time())
	if err != nil {
		return write.ScopeRevision{}, err
	}
	defer rows.Close()
	h := sha256.New()
	fmt.Fprintf(h, "%s\nphoto_day\n%s\n", epoch, scopeID)
	for rows.Next() {
		var id uuid.UUID
		var version int64
		if err := rows.Scan(&id, &version); err != nil {
			return write.ScopeRevision{}, err
		}
		fmt.Fprintf(h, "%s:%d\n", id, version)
	}
	if err := rows.Err(); err != nil {
		return write.ScopeRevision{}, err
	}
	return write.ScopeRevision{Kind: "photo_day", ScopeID: scopeID, Revision: fmt.Sprintf("sha256:%x", h.Sum(nil))}, nil
}

func (s *TxScope) RecordPhotoDayRevision(ctx context.Context, epoch, tripID uuid.UUID, day types.Date) error {
	r, err := s.PhotoDayRevision(ctx, epoch, tripID, day)
	if err != nil {
		return err
	}
	s.revisions = append(s.revisions, r)
	return nil
}
