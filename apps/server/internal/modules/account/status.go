package account

import "tripfolio/server/internal/foundation/apperr"

func StatusError(status string) error {
	if status == "banned" {
		return apperr.Forbidden("ACCOUNT_BANNED", "账号已被封禁，暂时无法使用")
	}
	return apperr.Forbidden("ACCOUNT_DELETING", "账号正在注销")
}
