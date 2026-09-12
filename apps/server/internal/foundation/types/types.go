// Package types 定义接口设计 1.3 的基础值类型：Date、LocalDateTime、Version。
// 它们以字符串形式进出 JSON，避免把数据库或 Go 的时间语义泄露给客户端。
package types

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Date 是 YYYY-MM-DD 形式的日期，不带时区。
type Date string

const dateLayout = "2006-01-02"

// ParseDate 严格解析日期，拒绝 2026-1-5 这类非规范形式。
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil || t.Format(dateLayout) != s {
		return "", fmt.Errorf("日期格式必须是 YYYY-MM-DD")
	}
	return Date(s), nil
}

// DateOf 取 t 在其所在时区的日期。
func DateOf(t time.Time) Date { return Date(t.Format(dateLayout)) }

// Time 返回 UTC 零点的时间，用于比较与数据库写入。
func (d Date) Time() time.Time {
	t, _ := time.Parse(dateLayout, string(d))
	return t
}

// String 实现 fmt.Stringer。
func (d Date) String() string { return string(d) }

// Valid 判断是否为规范日期。
func (d Date) Valid() bool {
	_, err := ParseDate(string(d))
	return err == nil
}

// Before 判断 d 是否早于 other。
func (d Date) Before(other Date) bool { return string(d) < string(other) }

// After 判断 d 是否晚于 other。
func (d Date) After(other Date) bool { return string(d) > string(other) }

// AddDays 返回 n 天后的日期。
func (d Date) AddDays(n int) Date { return DateOf(d.Time().AddDate(0, 0, n)) }

// TodayIn 返回 now 在 IANA 时区 tz 下的日期。
func TodayIn(now time.Time, tz string) (Date, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return "", fmt.Errorf("未知时区 %q", tz)
	}
	return DateOf(now.In(loc)), nil
}

// LocalDateTime 是 YYYY-MM-DDTHH:mm:ss 形式的当地日期时间，不带偏移，按旅行时区解释。
type LocalDateTime string

const localDateTimeLayout = "2006-01-02T15:04:05"

// ParseLocalDateTime 严格解析。
func ParseLocalDateTime(s string) (LocalDateTime, error) {
	t, err := time.Parse(localDateTimeLayout, s)
	if err != nil || t.Format(localDateTimeLayout) != s {
		return "", fmt.Errorf("时间格式必须是 YYYY-MM-DDTHH:mm:ss")
	}
	return LocalDateTime(s), nil
}

// LocalDateTimeOf 把无时区含义的 time.Time（数据库 timestamp）转为字符串。
func LocalDateTimeOf(t time.Time) LocalDateTime { return LocalDateTime(t.Format(localDateTimeLayout)) }

// Time 返回以 UTC 表示的裸时间，仅用于比较与数据库写入。
func (l LocalDateTime) Time() time.Time {
	t, _ := time.Parse(localDateTimeLayout, string(l))
	return t
}

// Date 返回日期部分。
func (l LocalDateTime) Date() Date { return Date(string(l)[:10]) }

// String 实现 fmt.Stringer。
func (l LocalDateTime) String() string { return string(l) }

// Version 是资源版本，JSON 中为正整数十进制字符串。
type Version int64

// MarshalJSON 输出字符串。
func (v Version) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatInt(int64(v), 10))
}

// UnmarshalJSON 接受字符串形式的正整数。
func (v *Version) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("版本必须是十进制字符串")
	}
	n, err := ParseVersion(s)
	if err != nil {
		return err
	}
	*v = n
	return nil
}

// ParseVersion 解析 "7" 这样的版本字符串。
func ParseVersion(s string) (Version, error) {
	if s == "" || s[0] == '0' {
		return 0, fmt.Errorf("版本必须是不带前导零的正整数")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("版本必须是正整数")
	}
	return Version(n), nil
}

// String 实现 fmt.Stringer。
func (v Version) String() string { return strconv.FormatInt(int64(v), 10) }
