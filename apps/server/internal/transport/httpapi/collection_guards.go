package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/collectionguard"
)

const collectionGuardsHeader = "X-Collection-Guards"
const collectionGuardsMaxBytes = 16 * 1024

// parseCollectionGuards returns nil for an absent header;
// an explicit [] remains non-nil. Ownership and freshness are checked in the writer.
func parseCollectionGuards(h http.Header) ([]collectionguard.Guard, error) {
	var values []string
	for name, entries := range h {
		if strings.EqualFold(name, collectionGuardsHeader) {
			if len(entries) != 1 || values != nil {
				return nil, malformedGuards()
			}
			values = entries
		}
	}
	if values == nil {
		return nil, nil
	}
	raw := values[0]
	if len(raw) > collectionGuardsMaxBytes || !utf8.ValidString(raw) {
		return nil, malformedGuards()
	}
	d := json.NewDecoder(strings.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('[') {
		return nil, malformedGuards()
	}
	guards := make([]collectionguard.Guard, 0)
	seen := map[collectionguard.Scope]bool{}
	for d.More() {
		token, err = d.Token()
		if err != nil || token != json.Delim('{') {
			return nil, malformedGuards()
		}
		fields := map[string]string{}
		for d.More() {
			token, err = d.Token()
			if err != nil {
				return nil, malformedGuards()
			}
			key, ok := token.(string)
			if !ok || (key != "kind" && key != "scope_id" && key != "revision") {
				return nil, malformedGuards()
			}
			if _, exists := fields[key]; exists {
				return nil, malformedGuards()
			}
			token, err = d.Token()
			value, ok := token.(string)
			if err != nil || !ok {
				return nil, malformedGuards()
			}
			fields[key] = value
		}
		token, err = d.Token()
		if err != nil || token != json.Delim('}') || len(fields) != 3 {
			return nil, malformedGuards()
		}
		g := collectionguard.Guard{Kind: fields["kind"], ScopeID: fields["scope_id"], Revision: fields["revision"]}
		if err := collectionguard.Validate(g); err != nil {
			return nil, err
		}
		scope := collectionguard.Scope{Kind: g.Kind, ScopeID: g.ScopeID}
		if seen[scope] {
			return nil, apperr.Unprocessable("INVALID_REFERENCE", "集合基线重复")
		}
		seen[scope] = true
		guards = append(guards, g)
	}
	token, err = d.Token()
	if err != nil || token != json.Delim(']') {
		return nil, malformedGuards()
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, malformedGuards()
	}
	return guards, nil
}

func malformedGuards() error {
	return apperr.BadRequest("MALFORMED_REQUEST", "集合基线请求头格式无效或超过16KiB")
}

// collectionGuardRequests only carries unverified REST preconditions. Services
// must determine the final required scopes and check them inside the write UOW.
func collectionGuardRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This boundary owns the REST context, including clearing inherited values
		// on native, public and read requests. It never constructs a proof.
		ctx, _ := collectionguard.WithRequest(r.Context(), nil)
		r = r.WithContext(ctx)
		present := false
		for name := range r.Header {
			if strings.EqualFold(name, collectionGuardsHeader) {
				present = true
				break
			}
		}
		if !present {
			next.ServeHTTP(w, r)
			return
		}
		if !acceptsCollectionGuards(r) {
			writeAppError(w, r, apperr.Unprocessable("INVALID_REFERENCE", "此操作不接受集合基线请求头"))
			return
		}
		if _, err := mustActor(r.Context()); err != nil {
			writeCollectionGuardError(w, r, err)
			return
		}
		guards, err := parseCollectionGuards(r.Header)
		if err != nil {
			writeCollectionGuardError(w, r, err)
			return
		}
		ctx, err = collectionguard.WithRequest(r.Context(), guards)
		if err != nil {
			writeCollectionGuardError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeCollectionGuardError(w http.ResponseWriter, r *http.Request, err error) {
	e, ok := apperr.As(err)
	if !ok {
		e = apperr.Internal(err)
	}
	writeAppError(w, r, e)
}

func acceptsCollectionGuards(r *http.Request) bool {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 || parts[0] != "" || parts[1] != "api" || parts[2] != "v1" {
		return false
	}
	if len(parts) == 5 && parts[3] == "expense-categories" && parts[4] != "" {
		return r.Method == http.MethodPatch
	}
	if parts[3] != "trips" || parts[4] == "" {
		return false
	}
	if len(parts) == 6 {
		return (parts[5] == "members" && r.Method == http.MethodPut) ||
			((parts[5] == "ledger-entries" || parts[5] == "ledger-import") && r.Method == http.MethodPost)
	}
	if len(parts) == 7 && parts[6] != "" {
		return ((parts[5] == "ledger-entries" || parts[5] == "photos") && r.Method == http.MethodPatch) ||
			(parts[5] == "itinerary-items" && parts[6] == "reorder" && r.Method == http.MethodPost)
	}
	return false
}
