package admin

import (
	"context"
	"net/url"
	"strings"

	"tripfolio/server/internal/foundation/apperr"
)

type SiteSettings struct {
	ShareBaseURL string `json:"share_base_url"`
	Version      int64  `json:"version"`
}

type SiteSettingsStore interface {
	SiteSettings(context.Context, Session, Audit, *SiteSettings) (SiteSettings, error)
}

func (s *Service) SiteSettings(ctx context.Context, sess Session, in *SiteSettings, info RequestInfo) (SiteSettings, error) {
	store, ok := s.store.(SiteSettingsStore)
	if !ok {
		return SiteSettings{}, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "站点设置暂不可用")
	}
	action := "site.settings.read"
	if in != nil {
		action = "site.settings.update"
		in.ShareBaseURL = strings.TrimRight(strings.TrimSpace(in.ShareBaseURL), "/")
		u, err := url.Parse(in.ShareBaseURL)
		if err != nil || len(in.ShareBaseURL) > 2048 || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" || strings.ContainsAny(in.ShareBaseURL, "\\,?#") {
			return SiteSettings{}, apperr.Unprocessable("VALIDATION_FAILED", "分享站点地址须为完整的 HTTP 或 HTTPS 地址，不包含路径、账号、查询参数或片段")
		}
		if in.Version < 1 {
			return SiteSettings{}, apperr.BadRequest("VALIDATION_FAILED", "设置版本无效，请刷新后重试")
		}
	}
	a := s.event(&sess, action, "success", info)
	if in != nil {
		a.Reason = "修改旅行分享站点地址"
	}
	out, err := store.SiteSettings(ctx, sess, a, in)
	if err != nil {
		if _, ok := apperr.As(err); ok {
			return SiteSettings{}, err
		}
		return SiteSettings{}, apperr.Internal(err)
	}
	return out, nil
}
