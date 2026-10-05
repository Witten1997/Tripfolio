package collectionguard

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"
)

func TestRequestFingerprintAndIsolation(t *testing.T) {
	original := sha256.Sum256([]byte("original"))
	ctx := context.Background()
	if Fingerprint(ctx, original) != original {
		t.Fatal("legacy fingerprint changed")
	}
	g := Guard{"members", trip, revision}
	h := Guard{"todo_order", trip, revision}
	input := []Guard{h, g}
	guarded, err := WithRequest(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	want := Fingerprint(guarded, original)
	input[0].Revision = "invalid"
	copy := Request(guarded)
	copy[0].Revision = "changed"
	if Fingerprint(guarded, original) != want || want == original {
		t.Fatal("guard snapshot changed")
	}
	reordered, err := WithRequest(ctx, []Guard{g, h})
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(reordered, original) != want {
		t.Fatal("order changed fingerprint")
	}
	g.Revision = "sha256:" + strings.Repeat("b", 64)
	changed, err := WithRequest(ctx, []Guard{g, h})
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(changed, original) == want {
		t.Fatal("revision missing from fingerprint")
	}
	empty, err := WithRequest(ctx, []Guard{})
	if err != nil {
		t.Fatal(err)
	}
	if Request(empty) == nil || Fingerprint(empty, original) == original {
		t.Fatal("explicit empty lost")
	}
	missing, err := WithRequest(guarded, nil)
	if err != nil {
		t.Fatal(err)
	}
	if Request(missing) != nil || Fingerprint(missing, original) != original {
		t.Fatal("absent header not preserved")
	}
	if _, err := WithRequest(ctx, []Guard{g, g}); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err := WithRequest(ctx, []Guard{{"unknown", trip, revision}}); err == nil {
		t.Fatal("invalid accepted")
	}
}
