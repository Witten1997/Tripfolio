// Package member 是旅行成员业务：成员名称、分摊百分比、排序与整体保存（接口设计 2.5、3.5；数据库设计表 25）。
// 本包不依赖 HTTP、pgx 或 sqlc；数据访问通过 ports.go 的接口由 PostgreSQL 适配器实现。
package member

import (
	"regexp"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/types"
)

// EntityType 是成员的同步实体类型（接口设计 4.2）。
const EntityType = "trip_member"

const (
	// SelfName 是创建旅行时自动生成的「我」的默认名称。
	SelfName = "我"
	// MaxMembers 是每趟旅行的成员上限。
	MaxMembers = 50
	// MaxNameChars 是名称长度上限。
	MaxNameChars = 30
)

// Resource 是成员的规范资源，也是同步日志与快照中的表示（接口设计 3.5 TripMember）。
// SharePercent 是 0–100 的十进制字符串（最多 2 位小数，去尾随零）。
type Resource struct {
	ID           uuid.UUID     `json:"id"`
	TripID       uuid.UUID     `json:"trip_id"`
	Name         string        `json:"name"`
	SharePercent string        `json:"share_percent"`
	SortOrder    int32         `json:"sort_order"`
	IsSelf       bool          `json:"is_self"`
	Version      types.Version `json:"version"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	DeletedAt    *time.Time    `json:"deleted_at"`
}

// Fields 是可局部更新的业务字段名，用于 changed_fields 与字段级合并。
var Fields = []string{"name", "share_percent", "sort_order"}

// NewSelf 构造创建旅行时同事务插入的「我」：100%、排第一。
func NewSelf(id, tripID uuid.UUID, now time.Time) Resource {
	return Resource{
		ID: id, TripID: tripID, Name: SelfName, SharePercent: "100", SortOrder: 0, IsSelf: true,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}

var percentPattern = regexp.MustCompile(`^(100(\.0{1,2})?|\d{1,2}(\.\d{1,2})?)$`)

// ParsePercent 解析并规范化百分比字符串：0–100、最多 2 位小数；返回去尾随零的规范形式。
func ParsePercent(raw string) (string, money.Decimal, bool) {
	if !percentPattern.MatchString(raw) {
		return "", money.Decimal{}, false
	}
	d, err := money.ParseDecimal(raw)
	if err != nil {
		return "", money.Decimal{}, false
	}
	return d.Format(0), d, true
}

// PercentSum 判断百分比之和是否恰好为 100。
func PercentSum(values []money.Decimal) (money.Decimal, bool) {
	total := money.Zero()
	for _, v := range values {
		total = total.Add(v)
	}
	return total, total.Cmp(money.MustDecimal("100")) == 0
}
