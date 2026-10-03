package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"tripfolio/server/internal/foundation/apperr"
	"unicode/utf8"
)

type contentBodyRule struct {
	fields   map[string]bool
	required []string
}

var contentBodyRules = map[string]contentBodyRule{
	"photos/Create":       {fields: map[string]bool{"id": false, "asset_id": false, "taken_at_local": true, "recorded_on": false, "caption": false, "sort_order": false, "place_name": false, "address": false, "latitude": true, "longitude": true, "asset": false}, required: []string{"id"}},
	"photos/Patch":        {fields: map[string]bool{"asset_id": false, "taken_at_local": true, "recorded_on": false, "caption": false, "sort_order": false, "place_name": false, "address": false, "latitude": true, "longitude": true}, required: []string{}},
	"reservations/Create": {fields: map[string]bool{"id": false, "kind": false, "title": false, "booking_reference": false, "transport_number": true, "provider_name": true, "start_local": true, "end_local": true, "origin": true, "destination": true, "address": false, "contact_name": true, "contact_phone": true, "notes": false}, required: []string{"id", "kind", "title"}},
	"reservations/Patch":  {fields: map[string]bool{"kind": false, "title": false, "booking_reference": false, "transport_number": true, "provider_name": true, "start_local": true, "end_local": true, "origin": true, "destination": true, "address": false, "contact_name": true, "contact_phone": true, "notes": false}, required: []string{}},
	"documents/Create":    {fields: map[string]bool{"id": false, "title": false, "notes": false, "asset_id": false, "reservation_id": true}, required: []string{"id", "title", "asset_id"}},
	"documents/Patch":     {fields: map[string]bool{"title": false, "notes": false, "asset_id": false, "reservation_id": true}, required: []string{}},
}

func validateContentObject(body []byte, rule contentBodyRule) error {
	if !utf8.Valid(body) {
		return apperr.Validation(apperr.Field("", "INVALID", "正文必须是有效 UTF-8"))
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return apperr.BadRequest("MALFORMED_REQUEST", "正文必须是 JSON 对象")
	}
	for key, value := range obj {
		nullable, ok := rule.fields[key]
		if !ok {
			return apperr.Validation(apperr.Field(key, "UNKNOWN_FIELD", "不允许的字段"))
		}
		if !nullable && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return apperr.Validation(apperr.Field(key, "NOT_NULL", "字段不可为空"))
		}
	}
	for _, key := range rule.required {
		if _, ok := obj[key]; !ok {
			return apperr.Validation(apperr.Field(key, "REQUIRED", "字段必填"))
		}
	}
	if raw, ok := obj["asset"]; ok {
		return validateContentObject(raw, contentBodyRule{fields: map[string]bool{"original_name": false, "expected_size": false, "declared_media_type": false, "client_sha256": false}, required: []string{"original_name", "expected_size", "declared_media_type"}})
	}
	return nil
}

// Validate only the slice 5 request bodies before the generated decoder discards field presence.
func contentRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 5 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "trips" {
			next.ServeHTTP(w, r)
			return
		}
		resource := parts[4]
		if resource != "photos" && resource != "reservations" && resource != "documents" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			tag := r.Header.Get("If-Match")
			if tag == "" {
				writeAppError(w, r, apperr.VersionRequired())
				return
			}
			valid := len(tag) >= 3 && tag[0] == '"' && tag[len(tag)-1] == '"' && tag[1] >= '1' && tag[1] <= '9'
			if valid {
				for _, c := range tag[1 : len(tag)-1] {
					if c < '0' || c > '9' {
						valid = false
						break
					}
				}
			}
			if !valid {
				requestError(w, r, apperr.BadRequest("MALFORMED_REQUEST", "If-Match 必须为带双引号的版本"))
				return
			}
		}
		op := ""
		if r.Method == http.MethodPost && len(parts) == 5 {
			op = "Create"
		}
		if r.Method == http.MethodPatch && len(parts) == 6 {
			op = "Patch"
		}
		if op == "" {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			WriteProblem(w, r, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "正文超过 1 MiB")
			return
		}
		if err := validateContentObject(body, contentBodyRules[resource+"/"+op]); err != nil {
			if e, ok := apperr.As(err); ok {
				writeAppError(w, r, e)
				return
			}
			requestError(w, r, err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}
