package db_test

import (
	"testing"

	"tripfolio/server/db"
)

func TestLatestVersionMatchesEmbeddedFiles(t *testing.T) {
	v, err := db.LatestVersion()
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if v < 1 {
		t.Fatalf("LatestVersion = %d, want >= 1", v)
	}
}
