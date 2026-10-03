package adminpg

import (
	"context"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/admin"
)

func (s *Store) ShareBaseURL(ctx context.Context) (string, error) {
	var value string
	err := s.pool.QueryRow(ctx, `SELECT share_base_url FROM admin_site_settings WHERE id=1`).Scan(&value)
	return value, err
}

func (s *Store) SiteSettings(ctx context.Context, sess admin.Session, a admin.Audit, in *admin.SiteSettings) (out admin.SiteSettings, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = lockControl(ctx, tx, sess, sess.AccountID, false); err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `SELECT share_base_url,version FROM admin_site_settings WHERE id=1 FOR UPDATE`).Scan(&out.ShareBaseURL, &out.Version)
	if err != nil {
		return out, err
	}
	if in != nil {
		if in.Version != out.Version {
			return out, apperr.Conflicted("VERSION_CONFLICT", "设置已变更，请刷新后重试")
		}
		a.Details = map[string]any{"before": out.ShareBaseURL, "after": in.ShareBaseURL}
		err = tx.QueryRow(ctx, `UPDATE admin_site_settings SET share_base_url=$1,version=version+1,updated_at=clock_timestamp() WHERE id=1 RETURNING share_base_url,version`, in.ShareBaseURL).Scan(&out.ShareBaseURL, &out.Version)
		if err != nil {
			return out, err
		}
	}
	return out, auditedControl(ctx, tx, a)
}
