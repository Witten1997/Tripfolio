package collectionbaseline

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/modules/account"
)

const cursorScope = "web_collection/v1"
const lifetime = 10 * time.Minute

type Service struct {
	reader Reader
	codec  paging.Codec
	clock  clock.Clock
}

func NewService(reader Reader, codec paging.Codec, clk clock.Clock) *Service {
	return &Service{reader: reader, codec: codec, clock: clk}
}

type cursor struct {
	Version   int       `json:"v"`
	Kind      string    `json:"kind"`
	ScopeID   string    `json:"scope_id"`
	Epoch     uuid.UUID `json:"sync_epoch"`
	Revision  string    `json:"revision"`
	After     uuid.UUID `json:"after_id"`
	ExpiresAt int64     `json:"expires_at"`
	Limit     int       `json:"limit"`
}

func unavailable() error {
	return apperr.New(503, "DEPENDENCY_UNAVAILABLE", "集合读取暂不可用")
}

func changed() error {
	return apperr.New(412, "COLLECTION_BASELINE_CHANGED", "集合资料已变化或读取已过期，请重新读取并核对")
}

func validScope(scope collectionguard.Scope) bool {
	if scope.Kind != "categories" && scope.Kind != "itinerary_day" && scope.Kind != "photo_day" {
		return false
	}
	// The shared validator owns canonical UUID and calendar-date syntax.
	return collectionguard.Validate(collectionguard.Guard{Kind: scope.Kind, ScopeID: scope.ScopeID,
		Revision: "sha256:" + strings.Repeat("0", 64)}) == nil
}

func (s *Service) List(ctx context.Context, a actor.Actor, q Query) (Page, error) {
	if a.AccountID == uuid.Nil || a.SessionID == uuid.Nil || !a.ClientKind.Valid() {
		return Page{}, apperr.Unauthorized("SESSION_EXPIRED", "请重新登录")
	}
	scope := collectionguard.Scope{Kind: q.Kind, ScopeID: q.ScopeID}
	if !validScope(scope) {
		return Page{}, apperr.Unprocessable("INVALID_REFERENCE", "集合范围无效")
	}
	if q.Kind == "categories" && q.ScopeID != a.AccountID.String() {
		return Page{}, apperr.NotFound()
	}
	limit, err := paging.Limit(q.Limit)
	if err != nil {
		return Page{}, err
	}
	if s.reader == nil || s.codec == nil || s.clock == nil {
		return Page{}, unavailable()
	}
	c := cursor{Version: 1, Kind: q.Kind, ScopeID: q.ScopeID, Limit: limit,
		ExpiresAt: s.clock.Now().UTC().Add(lifetime).Unix()}
	if q.Cursor != "" {
		if len(q.Cursor) > 4096 || s.codec.Decode(a.AccountID, cursorScope, q.Cursor, &c) != nil ||
			c.Version != 1 || c.Kind != q.Kind || c.ScopeID != q.ScopeID || c.Limit != limit ||
			c.Epoch == uuid.Nil || c.After == uuid.Nil || c.ExpiresAt <= 0 ||
			c.ExpiresAt > s.clock.Now().Add(lifetime).Unix() ||
			collectionguard.Validate(collectionguard.Guard{Kind: c.Kind, ScopeID: c.ScopeID, Revision: c.Revision}) != nil {
			return Page{}, paging.InvalidCursor()
		}
		if s.clock.Now().Unix() >= c.ExpiresAt {
			return Page{}, changed()
		}
	}
	var result Page
	err = s.reader.Read(ctx, a, func(v View) error {
		st, err := v.State(ctx)
		if err != nil {
			return err
		}
		if !st.SessionActive {
			return apperr.Unauthorized("SESSION_EXPIRED", "请重新登录")
		}
		if st.AccountStatus != "active" {
			return account.StatusError(st.AccountStatus)
		}
		if st.Epoch == uuid.Nil {
			return unavailable()
		}
		r, err := v.Revision(ctx, st.Epoch, scope)
		if err != nil {
			return err
		}
		if r.Kind != q.Kind || r.ScopeID != q.ScopeID ||
			collectionguard.Validate(collectionguard.Guard{Kind: r.Kind, ScopeID: r.ScopeID, Revision: r.Revision}) != nil {
			return unavailable()
		}
		if q.Cursor != "" && (c.Epoch != st.Epoch || c.Revision != r.Revision) {
			return changed()
		}
		entries, err := v.Entries(ctx, scope, c.After, limit+1)
		if err != nil {
			return err
		}
		if len(entries) > limit+1 {
			return unavailable()
		}
		previous := c.After
		for _, entry := range entries {
			if bytes.Compare(entry.ID[:], previous[:]) <= 0 || !validEntry(scope, entry) {
				return unavailable()
			}
			previous = entry.ID
		}
		result = Page{Kind: q.Kind, ScopeID: q.ScopeID, SyncEpoch: st.Epoch, Revision: r.Revision,
			ExpiresAt: time.Unix(c.ExpiresAt, 0).UTC(), Items: make([]json.RawMessage, 0, min(limit, len(entries)))}
		for _, entry := range entries[:min(limit, len(entries))] {
			result.Items = append(result.Items, bytes.Clone(entry.Data))
		}
		if len(entries) > limit {
			c.Epoch, c.Revision, c.After = st.Epoch, r.Revision, entries[limit-1].ID
			token, err := s.codec.Encode(a.AccountID, cursorScope, c)
			if err != nil {
				return apperr.Internal(err)
			}
			result.NextCursor = &token
		}
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if s.clock.Now().Unix() >= c.ExpiresAt {
		return Page{}, changed()
	}
	return result, nil
}

func validEntry(scope collectionguard.Scope, entry Entry) bool {
	var data struct {
		ID          string          `json:"id"`
		Version     string          `json:"version"`
		TripID      string          `json:"trip_id"`
		ScheduledOn string          `json:"scheduled_on"`
		RecordedOn  string          `json:"recorded_on"`
		DeletedAt   json.RawMessage `json:"deleted_at"`
	}
	if entry.ID == uuid.Nil || entry.Version <= 0 || json.Unmarshal(entry.Data, &data) != nil ||
		data.ID != entry.ID.String() || data.Version != strconv.FormatInt(entry.Version, 10) || string(data.DeletedAt) != "null" {
		return false
	}
	if scope.Kind == "categories" {
		return true
	}
	parts := strings.Split(scope.ScopeID, "/")
	day := data.ScheduledOn
	if scope.Kind == "photo_day" {
		day = data.RecordedOn
	}
	return data.TripID == parts[0] && day == parts[1]
}
