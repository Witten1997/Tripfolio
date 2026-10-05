package collectionguard

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sort"
)

type requestKey struct{}
type requestGuards struct{ guards []Guard }

// WithRequest captures a validated, immutable copy of REST preconditions. nil
// means no header; [] is an explicitly supplied empty header. It grants no access.
func WithRequest(ctx context.Context, guards []Guard) (context.Context, error) {
	if guards == nil {
		return context.WithValue(ctx, requestKey{}, requestGuards{}), nil
	}
	copyGuards := make([]Guard, len(guards))
	copy(copyGuards, guards)
	seen := make(map[Scope]bool, len(guards))
	for _, g := range copyGuards {
		if err := Validate(g); err != nil {
			return nil, err
		}
		s := Scope{g.Kind, g.ScopeID}
		if seen[s] {
			return nil, invalid()
		}
		seen[s] = true
	}
	sort.Slice(copyGuards, func(i, j int) bool {
		if copyGuards[i].Kind != copyGuards[j].Kind {
			return copyGuards[i].Kind < copyGuards[j].Kind
		}
		return copyGuards[i].ScopeID < copyGuards[j].ScopeID
	})
	return context.WithValue(ctx, requestKey{}, requestGuards{copyGuards}), nil
}

// Request returns a defensive copy, so callers cannot change a retry's guards.
func Request(ctx context.Context) []Guard {
	v, _ := ctx.Value(requestKey{}).(requestGuards)
	if v.guards == nil {
		return nil
	}
	guards := make([]Guard, len(v.guards))
	copy(guards, v.guards)
	return guards
}

// Fingerprint preserves every historical unguarded fingerprint byte-for-byte.
// Explicit preconditions use a separate domain and their original revisions.
func Fingerprint(ctx context.Context, original [32]byte) [32]byte {
	guards := Request(ctx)
	if guards == nil {
		return original
	}
	encoded, _ := json.Marshal(guards) // Guard has only string fields.
	h := sha256.New()
	h.Write([]byte("tripfolio/rest-collection-guards/v1\x00"))
	h.Write(original[:])
	h.Write([]byte{0})
	h.Write(encoded)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
