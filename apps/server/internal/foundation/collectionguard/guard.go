// Package collectionguard checks collection preconditions within the caller's account transaction.
package collectionguard

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
)

type Guard struct {
	Kind     string `json:"kind"`
	ScopeID  string `json:"scope_id"`
	Revision string `json:"revision"`
}

type Scope struct {
	Kind    string `json:"kind"`
	ScopeID string `json:"scope_id"`
}

var revisionPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func invalid() error { return apperr.Unprocessable("INVALID_REFERENCE", "集合基线或范围无效") }

// Validate checks wire syntax only. It does not establish ownership or freshness.
func Validate(g Guard) error {
	if !revisionPattern.MatchString(g.Revision) {
		return invalid()
	}
	_, err := scopeOwner(Scope{g.Kind, g.ScopeID})
	return err
}

func scopeOwner(s Scope) (uuid.UUID, error) {
	id := s.ScopeID
	switch s.Kind {
	case "categories", "members", "packing_order", "todo_order":
	case "itinerary_day", "photo_day":
		parts := strings.Split(s.ScopeID, "/")
		if len(parts) != 2 {
			return uuid.Nil, invalid()
		}
		date, err := time.Parse("2006-01-02", parts[1])
		if err != nil || date.Year() < 1 || date.Format("2006-01-02") != parts[1] {
			return uuid.Nil, invalid()
		}
		id = parts[0]
	default:
		return uuid.Nil, invalid()
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return uuid.Nil, invalid()
	}
	return parsed, nil
}

// Check must run after successful-receipt replay and before any new write/no-op result.
// enabled and required are trusted server decisions, never client flags. The caller
// holds the account lock. resolve must check actual account/trip ownership in that
// same transaction and return its complete, confirmed collection revision.
func Check(ctx context.Context, enabled bool, accountID uuid.UUID, provided []Guard, required []Scope, resolve func(context.Context, Scope) (string, error)) error {
	if accountID == uuid.Nil {
		return invalid()
	}
	wanted := make(map[Scope]struct{}, len(required))
	for _, s := range required {
		owner, err := scopeOwner(s)
		if err != nil {
			return err
		}
		if s.Kind == "categories" && owner != accountID {
			return invalid()
		}
		if _, exists := wanted[s]; exists {
			return invalid()
		}
		wanted[s] = struct{}{}
	}
	supplied := make(map[Scope]string, len(provided))
	for _, g := range provided {
		if err := Validate(g); err != nil {
			return err
		}
		s := Scope{g.Kind, g.ScopeID}
		if _, ok := wanted[s]; !ok {
			return invalid()
		}
		if _, duplicate := supplied[s]; duplicate {
			return invalid()
		}
		supplied[s] = g.Revision
	}
	if !enabled && provided == nil {
		return nil
	}
	for _, s := range required {
		if _, ok := supplied[s]; !ok {
			return apperr.New(428, "COLLECTION_BASE_REQUIRED", "缺少集合基线，请保留输入并刷新升级后核对")
		}
	}
	for _, s := range required {
		if err := ctx.Err(); err != nil {
			return err
		}
		if resolve == nil {
			return apperr.Internal(fmt.Errorf("collection resolver is required"))
		}
		current, err := resolve(ctx, s)
		if err != nil {
			return err
		}
		if !revisionPattern.MatchString(current) {
			return apperr.Internal(fmt.Errorf("invalid collection revision from resolver"))
		}
		if current != supplied[s] {
			return apperr.New(412, "COLLECTION_CONFLICT", "集合已发生变化，请保留输入并核对最新内容")
		}
	}
	return nil
}
