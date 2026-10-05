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

// parseCollectionGuards is not yet wired to routes. nil means an absent header;
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
