package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"tripfolio/server/internal/foundation/apperr"
)

func TestParseCollectionGuards(t *testing.T) {
	revision := "sha256:" + strings.Repeat("a", 64)
	object := fmt.Sprintf(`{"kind":"members","scope_id":"22222222-2222-4222-8222-222222222222","revision":%q}`, revision)
	for _, tc := range []struct {
		name, raw string
		status    int
	}{
		{"empty array", "[]", 0}, {"valid", "[" + object + "]", 0},
		{"null", "null", 400}, {"object", object, 400}, {"empty", "", 400},
		{"trailing", "[] true", 400}, {"trailing comma", "[" + object + ",]", 400},
		{"duplicate field", "[" + strings.Replace(object, `"kind":"members"`, `"kind":"members","kind":"members"`, 1) + "]", 400},
		{"escaped duplicate", "[" + strings.Replace(object, `"kind":"members"`, `"kind":"members","\u006bind":"members"`, 1) + "]", 400},
		{"unknown field", "[" + strings.Replace(object, `"kind":"members"`, `"kind":"members","enabled":true`, 1) + "]", 400},
		{"wrong case", "[" + strings.Replace(object, `"kind"`, `"Kind"`, 1) + "]", 400},
		{"null value", "[" + strings.Replace(object, `"members"`, `null`, 1) + "]", 400},
		{"missing", "[{}]", 400}, {"nested", "[[" + object + "]]", 400},
		{"duplicate scope", "[" + object + "," + object + "]", 422},
		{"bad revision", "[" + strings.Replace(object, revision, "bad", 1) + "]", 422},
		{"boundary", "[]" + strings.Repeat(" ", collectionGuardsMaxBytes-2), 0},
		{"oversized", "[]" + strings.Repeat(" ", collectionGuardsMaxBytes-1), 400},
		{"invalid utf8", "[\"" + string([]byte{0xff}) + "\"]", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCollectionGuards(http.Header{collectionGuardsHeader: []string{tc.raw}})
			if tc.status == 0 {
				if err != nil || got == nil {
					t.Fatalf("%v %v", got, err)
				}
				return
			}
			e, ok := apperr.As(err)
			if !ok || e.Status != tc.status {
				t.Fatalf("got %v want %d", err, tc.status)
			}
		})
	}
	got, err := parseCollectionGuards(nil)
	if err != nil || got != nil {
		t.Fatalf("%v %v", got, err)
	}
	for _, h := range []http.Header{
		{collectionGuardsHeader: []string{"[]", "[]"}},
		{collectionGuardsHeader: []string{"[]"}, "x-collection-guards": []string{"[]"}},
		{collectionGuardsHeader: []string{}},
	} {
		if _, err := parseCollectionGuards(h); err == nil {
			t.Fatal("accepted duplicate/empty header")
		}
	}
	got, err = parseCollectionGuards(http.Header{"x-collection-guards": []string{"[" + object + "]"}})
	if err != nil || len(got) != 1 || got[0].Revision != revision {
		t.Fatalf("%v %v", got, err)
	}
}
