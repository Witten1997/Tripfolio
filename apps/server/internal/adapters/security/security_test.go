package security_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/adapters/security"
	"tripfolio/server/internal/foundation/paging"
)

func testKeyring(t *testing.T) *security.Keyring {
	t.Helper()
	spec := "k2=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("b", 32))) +
		",k1=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	kr, err := security.ParseKeyring(spec)
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

func TestKeyringParsing(t *testing.T) {
	kr := testKeyring(t)
	if kr.CurrentKID() != "k2" {
		t.Fatalf("current = %s", kr.CurrentKID())
	}
	a, _ := kr.Derive("k1", "jwt")
	b, _ := kr.Derive("k1", "cursor")
	if string(a) == string(b) {
		t.Fatal("different purposes must derive different keys")
	}
	for _, bad := range []string{"", "k1=short", "nokey", "k1=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32))) + ",k1=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))} {
		if _, err := security.ParseKeyring(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestMACRoundTrip(t *testing.T) {
	kr := testKeyring(t)
	kid, mac, err := kr.MAC("challenge", []byte("id:123456"))
	if err != nil {
		t.Fatal(err)
	}
	if !kr.VerifyMAC(kid, "challenge", []byte("id:123456"), mac) {
		t.Fatal("MAC should verify")
	}
	if kr.VerifyMAC(kid, "challenge", []byte("id:123457"), mac) || kr.VerifyMAC("k1", "challenge", []byte("id:123456"), mac) {
		t.Fatal("MAC must not verify with other data or key")
	}
}

func TestPasswordHashVerify(t *testing.T) {
	h := security.NewPasswordHasher(2)
	encoded, err := h.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=1$") {
		t.Fatalf("unexpected PHC prefix: %s", encoded)
	}
	ok, err := h.Verify(encoded, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	ok, _ = h.Verify(encoded, "wrong")
	if ok {
		t.Fatal("wrong password must not verify")
	}
	if _, err := h.Verify("$bcrypt$x", "x"); err == nil {
		t.Fatal("foreign format must error")
	}
}

func TestAccessTokenRoundTrip(t *testing.T) {
	kr := testKeyring(t)
	issuer := security.NewTokenIssuer(kr, 15*time.Minute)
	now := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	acc, sid := uuid.New(), uuid.New()
	raw, err := issuer.Issue(acc, sid, now)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := issuer.Parse(raw, now.Add(5*time.Minute))
	if err != nil || claims.AccountID != acc || claims.SessionID != sid {
		t.Fatalf("parse: %+v, %v", claims, err)
	}
	if _, err := issuer.Parse(raw, now.Add(16*time.Minute)); err == nil {
		t.Fatal("expired token must be rejected")
	}
	other, _ := security.ParseKeyring("z=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("z", 32))))
	if _, err := security.NewTokenIssuer(other, time.Minute).Parse(raw, now); err == nil {
		t.Fatal("token signed by unknown kid must be rejected")
	}
}

func TestSixDigitCode(t *testing.T) {
	for i := 0; i < 50; i++ {
		code, err := security.SixDigitCode()
		if err != nil || len(code) != 6 {
			t.Fatalf("code = %q, err = %v", code, err)
		}
	}
}

func TestCursorCodecRoundTripAndTamper(t *testing.T) {
	kr, err := security.ParseKeyring("k1=" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("c", 32))))
	if err != nil {
		t.Fatal(err)
	}
	codec := security.NewCursorCodec(kr)
	account := uuid.New()
	type pos struct {
		Date string    `json:"d"`
		ID   uuid.UUID `json:"id"`
	}
	in := pos{Date: "2026-10-01", ID: uuid.New()}
	token, err := codec.Encode(account, "trips|sort=start_date_desc", in)
	if err != nil {
		t.Fatal(err)
	}
	var out pos
	if err := codec.Decode(account, "trips|sort=start_date_desc", token, &out); err != nil || out != in {
		t.Fatalf("round trip: %+v, %v", out, err)
	}
	if err := codec.Decode(uuid.New(), "trips|sort=start_date_desc", token, &out); !errors.Is(err, paging.ErrInvalidCursor) {
		t.Fatalf("other account must fail: %v", err)
	}
	if err := codec.Decode(account, "trips|sort=updated_at_desc", token, &out); !errors.Is(err, paging.ErrInvalidCursor) {
		t.Fatalf("other scope must fail: %v", err)
	}
	body, sig, _ := strings.Cut(token, ".")
	tampered := body[:len(body)-2] + "AA." + sig
	if err := codec.Decode(account, "trips|sort=start_date_desc", tampered, &out); !errors.Is(err, paging.ErrInvalidCursor) {
		t.Fatalf("tampered body must fail: %v", err)
	}
	if err := codec.Decode(account, "trips|sort=start_date_desc", "no-dot", &out); !errors.Is(err, paging.ErrInvalidCursor) {
		t.Fatalf("malformed must fail: %v", err)
	}
}
