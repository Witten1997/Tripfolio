package config_test

import (
	"strings"
	"testing"

	"tripfolio/server/internal/config"
)

func minimalProdEnv() map[string]string {
	return map[string]string{
		"TRIPFOLIO_ENV":                  "prod",
		"TRIPFOLIO_DATABASE_URL":         "postgres://u:p@localhost:5432/db",
		"TRIPFOLIO_KEYRING":              "k1=" + strings.Repeat("A", 44),
		"TRIPFOLIO_WEB_BASE_URL":         "https://trip.example.com",
		"TRIPFOLIO_AMAP_WEB_SERVICE_KEY": "amap-key",
	}
}

func TestProdAllowsMissingOrPartialObjectStoreWithoutMail(t *testing.T) {
	fields := map[string]string{
		"TRIPFOLIO_OBJECTSTORE_ENDPOINT":          "https://oss-cn-hangzhou.aliyuncs.com",
		"TRIPFOLIO_OBJECTSTORE_BUCKET":            "tripfolio",
		"TRIPFOLIO_OBJECTSTORE_ACCESS_KEY_ID":     "ak",
		"TRIPFOLIO_OBJECTSTORE_SECRET_ACCESS_KEY": "sk",
	}
	cases := []string{"all"}
	for key := range fields {
		cases = append(cases, key)
	}
	for _, missing := range cases {
		t.Run(missing, func(t *testing.T) {
			env := minimalProdEnv()
			for key, value := range fields {
				if missing == "all" || missing == key {
					value = ""
				}
				env[key] = value
			}
			cfg, err := config.Load(envFrom(env))
			if err != nil {
				t.Fatalf("prod 缺少邮件和对象存储应可启动: %v", err)
			}
			if cfg.ObjectStore.Configured() || cfg.Mail.Driver != "disabled" {
				t.Fatal("未配置的对象存储和邮件应禁用")
			}
		})
	}
}

func TestMailConfiguration(t *testing.T) {
	cases := []struct {
		name, env, driver, host, from, wantDriver, wantError string
	}{
		{name: "prod default", env: "prod", wantDriver: "disabled"},
		{name: "prod blank", env: "prod", driver: " ", wantDriver: "disabled"},
		{name: "prod disabled", env: "prod", driver: "disabled", wantDriver: "disabled"},
		{name: "dev default", env: "dev", wantDriver: "log"},
		{name: "test default", env: "test", wantDriver: "log"},
		{name: "dev disabled", env: "dev", driver: "disabled", wantDriver: "disabled"},
		{name: "prod log rejected", env: "prod", driver: "log", wantError: "MAIL_DRIVER"},
		{name: "unknown driver rejected", env: "prod", driver: "unknown", wantError: "MAIL_DRIVER"},
		{name: "smtp empty rejected", env: "prod", driver: "smtp", wantError: "MAIL_SMTP_HOST"},
		{name: "smtp missing host", env: "prod", driver: "smtp", from: "no-reply@example.com", wantError: "MAIL_SMTP_HOST"},
		{name: "smtp missing from", env: "prod", driver: "smtp", host: "smtp.example.com", wantError: "MAIL_FROM"},
		{name: "smtp configured", env: "prod", driver: "smtp", host: "smtp.example.com", from: "no-reply@example.com", wantDriver: "smtp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := minimalProdEnv()
			env["TRIPFOLIO_ENV"] = tc.env
			env["TRIPFOLIO_MAIL_DRIVER"] = tc.driver
			env["TRIPFOLIO_MAIL_SMTP_HOST"] = tc.host
			env["TRIPFOLIO_MAIL_FROM"] = tc.from
			cfg, err := config.Load(envFrom(env))
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want %s error, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Mail.Driver != tc.wantDriver {
				t.Fatalf("driver = %q, want %q", cfg.Mail.Driver, tc.wantDriver)
			}
		})
	}
}
