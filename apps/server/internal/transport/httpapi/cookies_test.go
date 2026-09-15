package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 浏览器对 POST 一律带 Origin（即使同源），而来源是「协议＋主机＋端口」的完整值。
// 部署时最容易踩的坑就是站点在非默认端口、允许列表却漏了端口，这里把规则与提示都固定下来。
func TestCheckOrigin(t *testing.T) {
	allowed := []string{"https://trip.example.com:8443", "https://localhost"}

	cases := []struct {
		name    string
		origin  string
		wantErr bool
	}{
		{"允许的来源（带端口）", "https://trip.example.com:8443", false},
		{"大小写与末尾斜杠不影响比对", "HTTPS://Trip.Example.com:8443/", false},
		{"Capacitor 来源在列表内", "https://localhost", false},
		{"非浏览器请求没有 Origin 时放行", "", false},
		{"漏写端口的来源要被拒", "https://trip.example.com", true},
		{"端口不一致要被拒", "https://trip.example.com:9443", true},
		{"其它站点要被拒", "https://evil.example.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/email-challenges", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			err := checkOrigin(req, allowed)
			if tc.wantErr && err == nil {
				t.Fatalf("来源 %q 应被拒绝", tc.origin)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("来源 %q 应被允许，得到 %v", tc.origin, err)
			}
		})
	}
}

func TestCheckOriginRejectionExplainsHowToFix(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/email-challenges", nil)
	req.Header.Set("Origin", "https://trip.example.com:8443")

	err := checkOrigin(req, []string{"https://trip.example.com"}) // 列表漏了端口
	if err == nil {
		t.Fatal("漏写端口的允许列表应当拒绝该来源")
	}
	message := err.Error()
	for _, want := range []string{"CSRF_FAILED", "https://trip.example.com:8443", "TRIPFOLIO_CORS_ORIGINS", "端口"} {
		if !strings.Contains(message, want) {
			t.Errorf("提示应包含 %q：%s", want, message)
		}
	}
}
