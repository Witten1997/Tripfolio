package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSyncReleaseEvidence(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "tripfolio")
	if err := os.WriteFile(exe, []byte("unit artifact, not final cmd"), 0600); err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{"index.html": {Data: []byte("<html>unit</html>")}, "assets/app.js": {Data: []byte("app")}}
	facts, err := releaseFacts(context.Background(), exe, assets)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(filepath.Dir(exe), releaseDirectory)
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) releaseReference {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		return releaseReference{name, hex.EncodeToString(sum[:])}
	}
	writeJSON := func(name string, value any) releaseReference {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return write(name, data)
	}
	commit := strings.Repeat("a", 40)
	log := write("build.log", []byte("reviewed build log"))
	build := writeJSON("build.json", releaseBuild{commit, facts, log})
	report := write("report.log", []byte("reviewed report, fixture only"))
	base := releaseManifest{Schema: 1, Profile: releaseProfile, SourceCommit: commit, Artifact: facts, Build: build}
	for _, id := range requiredReleaseSuites {
		base.Suites = append(base.Suites, releaseSuite{ID: id, Status: "passed", SourceCommit: commit, ExecutableSHA256: facts.ExecutableSHA256, WebTreeSHA256: facts.WebTreeSHA256, Report: report})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*releaseManifest)
	}{
		{"valid", func(*releaseManifest) {}},
		{"schema", func(m *releaseManifest) { m.Schema = 2 }},
		{"profile", func(m *releaseManifest) { m.Profile = "other" }},
		{"source", func(m *releaseManifest) { m.SourceCommit = "short" }},
		{"binary", func(m *releaseManifest) { m.Artifact.ExecutableSHA256 = strings.Repeat("0", 64) }},
		{"web", func(m *releaseManifest) { m.Artifact.WebTreeSHA256 = strings.Repeat("0", 64) }},
		{"missing_suite", func(m *releaseManifest) { m.Suites = m.Suites[:4] }},
		{"duplicate", func(m *releaseManifest) { m.Suites[0].ID = m.Suites[1].ID }},
		{"blocked", func(m *releaseManifest) { m.Suites[0].Status = "blocked" }},
		{"report_hash", func(m *releaseManifest) { m.Suites[0].Report.SHA256 = strings.Repeat("0", 64) }},
		{"missing_report", func(m *releaseManifest) { m.Suites[0].Report.Path = "missing" }},
		{"traversal", func(m *releaseManifest) { m.Suites[0].Report.Path = "../report.log" }},
		{"absolute", func(m *releaseManifest) { m.Suites[0].Report.Path = "/report.log" }},
		{"windows_absolute", func(m *releaseManifest) { m.Suites[0].Report.Path = "C:/report.log" }},
		{"old_source_no_equivalence", func(m *releaseManifest) { m.Suites[0].SourceCommit = strings.Repeat("b", 40) }},
		{"old_artifact_no_equivalence", func(m *releaseManifest) { m.Suites[0].ExecutableSHA256 = strings.Repeat("b", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.Suites = append([]releaseSuite(nil), base.Suites...)
			tc.mutate(&m)
			writeJSON("manifest.json", m)
			err := verifySyncRelease(context.Background(), exe, assets)
			if (err == nil) != (tc.name == "valid") {
				t.Fatalf("unexpected acceptance: %v", err)
			}
		})
	}
	old := base
	old.Suites = append([]releaseSuite(nil), base.Suites...)
	old.Suites[0].SourceCommit = strings.Repeat("b", 40)
	equivalence := write("equivalence.log", []byte("reviewed source hash/diff equivalence"))
	reuse := releaseReuse{old.Suites[0].SourceCommit, facts.ExecutableSHA256, facts.WebTreeSHA256, report.SHA256, commit, facts, equivalence}
	ref := writeJSON("reuse.json", reuse)
	old.Suites[0].Reuse = &ref
	writeJSON("manifest.json", old)
	if err = verifySyncRelease(context.Background(), exe, assets); err != nil {
		t.Fatalf("reviewed reuse: %v", err)
	}
	reuse.ReportSHA256 = strings.Repeat("c", 64)
	ref = writeJSON("reuse.json", reuse)
	old.Suites[0].Reuse = &ref
	writeJSON("manifest.json", old)
	if verifySyncRelease(context.Background(), exe, assets) == nil {
		t.Fatal("unrelated reuse accepted")
	}
	writeJSON("manifest.json", base)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if verifySyncRelease(ctx, exe, assets) == nil {
		t.Fatal("canceled verification accepted")
	}
	if err = os.WriteFile(exe, []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	if verifySyncRelease(context.Background(), exe, assets) == nil {
		t.Fatal("replaced executable accepted")
	}
}

func TestSyncReleaseTreeAndStrictJSON(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "tripfolio")
	if err := os.WriteFile(exe, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	a := fstest.MapFS{"index.html": {Data: []byte("index")}, "a.js": {Data: []byte("a")}}
	b := fstest.MapFS{"a.js": {Data: []byte("a")}, "index.html": {Data: []byte("index")}}
	first, err := releaseFacts(context.Background(), exe, a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := releaseFacts(context.Background(), exe, b)
	if err != nil || first != second {
		t.Fatal("unstable tree hash")
	}
	b["a.js"].Data = []byte("b")
	second, err = releaseFacts(context.Background(), exe, b)
	if err != nil || first.WebTreeSHA256 == second.WebTreeSHA256 {
		t.Fatal("content ignored")
	}
	delete(b, "a.js")
	b["b.js"] = &fstest.MapFile{Data: []byte("a")}
	second, err = releaseFacts(context.Background(), exe, b)
	if err != nil || first.WebTreeSHA256 == second.WebTreeSHA256 {
		t.Fatal("path ignored")
	}
	if _, err = releaseFacts(context.Background(), exe, fstest.MapFS{}); err == nil {
		t.Fatal("missing web accepted")
	}
	for _, raw := range []string{`{"schema":1,"schema":1}`, `{"schema":2,"SCHEMA":1}`, `{"schema":1,"unknown":1}`, `{"schema":1} {}`, `{"schema":"1"}`} {
		var m releaseManifest
		if strictReleaseJSON([]byte(raw), &m) == nil {
			t.Fatal("non-strict JSON accepted")
		}
	}
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = os.WriteFile(filepath.Join(dir, "big"), []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readReleaseFile(context.Background(), root, "big", 4); err == nil {
		t.Fatal("oversized report accepted")
	}
	if err = os.Symlink(exe, filepath.Join(dir, "linked")); err == nil {
		if _, err = readReleaseFile(context.Background(), root, "linked", 1024); err == nil {
			t.Fatal("symlink accepted")
		}
	}
}
