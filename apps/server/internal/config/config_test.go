package config_test

import (
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"tripfolio/server/internal/config"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL": "postgres://u:p@localhost:5432/db",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want dev", cfg.Env)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.DBMaxConns != 10 {
		t.Errorf("DBMaxConns = %d, want 10", cfg.DBMaxConns)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 15s", cfg.ShutdownTimeout)
	}
	if want := []string{"http://localhost:5173", "https://localhost"}; !slices.Equal(cfg.CORSOrigins, want) {
		t.Errorf("CORSOrigins = %v, want %v", cfg.CORSOrigins, want)
	}
	if cfg.WorkerMaxJobs != 20 {
		t.Errorf("WorkerMaxJobs = %d, want 20", cfg.WorkerMaxJobs)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	_, err := config.Load(envFrom(map[string]string{}))
	if err == nil || !strings.Contains(err.Error(), "TRIPFOLIO_DATABASE_URL") {
		t.Fatalf("want error mentioning TRIPFOLIO_DATABASE_URL, got %v", err)
	}
}

func TestLoadRejectsInvalidValuesTogether(t *testing.T) {
	_, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL":     "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_ENV":              "staging",
		"TRIPFOLIO_LOG_LEVEL":        "loud",
		"TRIPFOLIO_SHUTDOWN_TIMEOUT": "soon",
		"TRIPFOLIO_DB_MAX_CONNS":     "0",
		"TRIPFOLIO_CORS_ORIGINS":     "localhost:5173",
	}))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	for _, key := range []string{"ENV", "LOG_LEVEL", "SHUTDOWN_TIMEOUT", "DB_MAX_CONNS", "CORS_ORIGINS"} {
		if !strings.Contains(err.Error(), "TRIPFOLIO_"+key) {
			t.Errorf("error should mention %s, got: %v", key, err)
		}
	}
}

func TestLoadSplitsAndTrimsCORSOrigins(t *testing.T) {
	cfg, err := config.Load(envFrom(map[string]string{
		"TRIPFOLIO_DATABASE_URL": "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_CORS_ORIGINS": " https://app.example.com , , https://localhost ",
		"TRIPFOLIO_LOG_LEVEL":    "WARN",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []string{"https://app.example.com", "https://localhost"}; !slices.Equal(cfg.CORSOrigins, want) {
		t.Errorf("CORSOrigins = %v, want %v", cfg.CORSOrigins, want)
	}
	if cfg.LogLevel != slog.LevelWarn {
		t.Errorf("LogLevel = %v, want warn", cfg.LogLevel)
	}
}
