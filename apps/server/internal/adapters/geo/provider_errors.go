package geo

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	geodata "tripfolio/server/internal/modules/geo"
)

// 只保留官方数字错误码，不回传或记录供应商 info 中可能夹带的请求内容。
func providerError(code string) error {
	code = strings.TrimSpace(code)
	if len(code) != 5 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return fmt.Errorf("高德响应包含无效错误码")
	}
	cause := fmt.Errorf("高德请求失败：infocode=%s", code)
	switch code {
	case "20800", "20801", "20802", "20803":
		return fmt.Errorf("%w: %w", ErrNoResult, cause)
	case "10014", "10015", "10019", "10020", "10021", "10029":
		return errRateLimited{retryAfter: time.Second, cause: cause}
	case "10004":
		return errRateLimited{retryAfter: time.Minute, cause: cause}
	case "10003", "10044", "10045", "40000", "40003":
		return fmt.Errorf("%w: %w", ErrQuotaExceeded, cause)
	case "10001", "10002", "10005", "10006", "10007", "10008", "10009", "10010", "10011", "10012", "10013", "10026", "10041", "40002", "20011":
		return fmt.Errorf("%w: %w", geodata.ErrConfiguration, cause)
	default:
		// SERVER_IS_BUSY、网关或引擎暂时异常由模块返回可恢复的 503。
		return cause
	}
}

func upstreamRetryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 && seconds <= 86400 {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		if delay := time.Until(date); delay > 0 {
			if delay > 24*time.Hour {
				return 24 * time.Hour
			}
			return delay
		}
	}
	return time.Second
}

type errTemporary struct {
	cause      error
	retryAfter time.Duration
}

func (e errTemporary) Error() string                      { return e.cause.Error() }
func (e errTemporary) Unwrap() error                      { return e.cause }
func (e errTemporary) TemporaryRetryAfter() time.Duration { return e.retryAfter }
