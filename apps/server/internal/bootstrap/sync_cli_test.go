package bootstrap

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"tripfolio/server/internal/config"
)

func TestSyncCLIInput(t *testing.T) {
	args := []string{"activate", "--account", uuid.NewString(), "--snapshot", uuid.NewString(), "--proof-file", "-"}
	if _, err := parseActivationArguments(args); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{nil, {"activate"}, append(append([]string{}, args...), "secret"), {"activate", "--account", args[2], "--account", args[4], "--proof-file", "-"}, {"activate", "--account", "bad-secret", "--snapshot", args[4], "--proof-file", "-"}, {"activate", "--ready", "secret", "--snapshot", args[4], "--proof-file", "-"}} {
		if _, err := parseActivationArguments(bad); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("bad args accepted or leaked")
		}
	}
	ctx := context.Background()
	for _, value := range []string{"", " ", "x\ny", strings.Repeat("x", 8193)} {
		if _, err := readActivationProof(ctx, "-", strings.NewReader(value)); err == nil {
			t.Fatal("bad proof input accepted")
		}
	}
	if proof, err := readActivationProof(ctx, "-", strings.NewReader("opaque\r\n")); err != nil || proof != "opaque" {
		t.Fatal("stdin failed")
	}
	path := filepath.Join(t.TempDir(), "proof")
	if err := os.WriteFile(path, []byte("opaque"), 0600); err != nil {
		t.Fatal(err)
	}
	if proof, err := readActivationProof(ctx, path, nil); err != nil || proof != "opaque" {
		t.Fatal("proof file failed")
	}
	for _, key := range []string{"", "secret-invalid-key"} {
		var out bytes.Buffer
		err := RunSyncCLI(ctx, config.Config{Keyring: key, DatabaseURL: "secret-dsn"}, args, strings.NewReader("secret-proof"), &out)
		if err == nil || strings.Contains(err.Error(), "secret") || out.Len() != 0 {
			t.Fatal("config rejection leaked or wrote")
		}
	}
	var out bytes.Buffer
	if err := RunSyncReleaseInfo(ctx, []string{"release-info", "secret"}, &out); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("release args leaked")
	}
}
