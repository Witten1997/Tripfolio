package adminpg

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"tripfolio/server/internal/modules/admin"
)

func (s *Store) Overview(ctx context.Context, now time.Time) (admin.Overview, error) {
	result := admin.Overview{AsOf: now, Signups: []admin.SignupDay{}}
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM accounts),
		(SELECT count(*) FROM accounts WHERE created_at>=date_trunc('day',$1::timestamptz AT TIME ZONE 'Asia/Shanghai') AT TIME ZONE 'Asia/Shanghai' AND created_at<=$1),
		(SELECT count(*) FROM trips WHERE deleted_at IS NULL),
		(SELECT count(*) FROM river_job WHERE state IN ('retryable','discarded'))`, now).Scan(&result.Users, &result.NewUsersToday, &result.Trips, &result.FailedJobs)
	if err != nil {
		return result, err
	}
	rows, err := s.pool.Query(ctx, `SELECT to_char(d.day,'YYYY-MM-DD'),count(a.id)
		FROM generate_series((($1::timestamptz AT TIME ZONE 'Asia/Shanghai')::date-6)::timestamp,(($1::timestamptz AT TIME ZONE 'Asia/Shanghai')::date)::timestamp,interval '1 day') d(day)
		LEFT JOIN accounts a ON a.created_at>=d.day AT TIME ZONE 'Asia/Shanghai' AND a.created_at<(d.day+interval '1 day') AT TIME ZONE 'Asia/Shanghai' AND a.created_at<=$1
		GROUP BY d.day ORDER BY d.day`, now)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var day admin.SignupDay
		if err := rows.Scan(&day.Date, &day.Count); err != nil {
			return result, err
		}
		result.Signups = append(result.Signups, day)
	}
	return result, rows.Err()
}

const userColumns = `a.id,a.email,a.nickname,a.status,
	EXISTS(SELECT 1 FROM admin_principals p WHERE p.account_id=a.id AND p.revoked_at IS NULL),a.created_at,
	(SELECT max(last_seen_at) FROM account_sessions WHERE account_id=a.id),
	(SELECT count(*) FROM trips t WHERE t.account_id=a.id AND t.deleted_at IS NULL)`

func scanUser(row interface{ Scan(...any) error }) (admin.User, error) {
	var user admin.User
	err := row.Scan(&user.ID, &user.Email, &user.Nickname, &user.Status, &user.IsSuperAdmin, &user.CreatedAt, &user.LastSeenAt, &user.TripCount)
	return user, err
}

func (s *Store) Users(ctx context.Context, f admin.UserFilter) (admin.UserPage, error) {
	result := admin.UserPage{Data: []admin.User{}, Page: f.Page, PageSize: f.PageSize}
	search := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Query)
	const where = ` WHERE ($1='' OR a.email ILIKE '%'||$1||'%' OR a.nickname ILIKE '%'||$1||'%' OR a.id::text=$3) AND ($2='' OR a.status=$2)
		AND (nullif($4,'')::date IS NULL OR a.created_at>=nullif($4,'')::date::timestamp AT TIME ZONE 'Asia/Shanghai')
		AND (nullif($5,'')::date IS NULL OR a.created_at<(nullif($5,'')::date+1)::timestamp AT TIME ZONE 'Asia/Shanghai')`
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM accounts a`+where, search, f.Status, f.Query, f.RegisteredFrom, f.RegisteredTo).Scan(&result.Total)
	if err != nil {
		return result, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM accounts a`+where+` ORDER BY a.created_at DESC,a.id DESC LIMIT $6 OFFSET $7`, search, f.Status, f.Query, f.RegisteredFrom, f.RegisteredTo, f.PageSize, (f.Page-1)*f.PageSize)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return result, err
		}
		result.Data = append(result.Data, user)
	}
	return result, rows.Err()
}

func (s *Store) User(ctx context.Context, id uuid.UUID, now time.Time) (admin.UserDetail, bool, error) {
	result := admin.UserDetail{Sessions: []admin.UserSession{}}
	user, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM accounts a WHERE a.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	result.Account = user
	rows, err := s.pool.Query(ctx, `SELECT id,client_kind,coalesce(device_name,''),created_at,last_seen_at,expires_at
		FROM account_sessions WHERE account_id=$1 AND revoked_at IS NULL AND expires_at>$2 ORDER BY last_seen_at DESC,id DESC`, id, now)
	if err != nil {
		return result, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var sess admin.UserSession
		if err := rows.Scan(&sess.ID, &sess.ClientKind, &sess.DeviceName, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt); err != nil {
			return result, false, err
		}
		result.Sessions = append(result.Sessions, sess)
	}
	return result, true, rows.Err()
}
