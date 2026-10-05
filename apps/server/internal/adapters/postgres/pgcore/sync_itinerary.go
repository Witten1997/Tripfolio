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

// ItineraryDayRevision reads the complete live day under the Writer account lock.
func (s *TxScope) ItineraryDayRevision(ctx context.Context, epoch, tripID uuid.UUID, date types.Date) (write.ScopeRevision, error) {
	var out write.ScopeRevision
	if epoch == uuid.Nil || tripID == uuid.Nil {
		return out, apperr.Unprocessable("INVALID_REFERENCE", "日期集合范围无效")
	}
	if _, err := types.ParseDate(string(date)); err != nil || len(date) != 10 {
		return out, apperr.Unprocessable("INVALID_REFERENCE", "日期集合范围无效")
	}
	scopeID := tripID.String() + "/" + string(date)
	rows, err := s.Tx.Query(ctx, `SELECT id,version FROM itinerary_items WHERE account_id=$1 AND trip_id=$2 AND scheduled_on=$3 AND deleted_at IS NULL ORDER BY id`, s.AccountID, tripID, date.Time())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	h := sha256.New()
	fmt.Fprintf(h, "%s\nitinerary_day\n%s\n", epoch, scopeID)
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
	return write.ScopeRevision{Kind: "itinerary_day", ScopeID: scopeID, Revision: fmt.Sprintf("sha256:%x", h.Sum(nil))}, nil
}

func (s *TxScope) RecordItineraryDayRevision(ctx context.Context, epoch, tripID uuid.UUID, date types.Date) error {
	r, err := s.ItineraryDayRevision(ctx, epoch, tripID, date)
	if err != nil {
		return err
	}
	s.revisions = append(s.revisions, r)
	return nil
}
