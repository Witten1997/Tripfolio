// Package finance 是账单业务：账号级账单分类、账目与退款、开支统计。
// 本包不依赖 HTTP、pgx 或 sqlc；数据访问通过 ports.go 的接口由 PostgreSQL 适配器实现。
package finance

import (
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/types"
)

// EntityTypeCategory 是账单分类的同步实体类型。
const EntityTypeCategory = "expense_category"

// CategoryResource 是账单分类的对外资源，也是同步日志与快照中的表示（接口设计 3.5）。
type CategoryResource struct {
	ID        uuid.UUID     `json:"id"`
	Name      string        `json:"name"`
	Icon      *string       `json:"icon"`
	SortOrder int32         `json:"sort_order"`
	IsPreset  bool          `json:"is_preset"`
	Version   types.Version `json:"version"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	DeletedAt *time.Time    `json:"deleted_at"`
}

// Preset 是注册时种入的预设分类。
type Preset struct {
	Name string
	Icon string
}

// PresetCategories 返回接口设计 3.5 规定的六个预设，顺序即 sort_order。
func PresetCategories() []Preset {
	return []Preset{
		{"交通", "transport"}, {"住宿", "lodging"}, {"美食", "food"},
		{"景点", "attraction"}, {"购物", "shopping"}, {"其他", "other"},
	}
}

// CategoryIcons 是可用的图标键集合，与 metadata 一致。
var CategoryIcons = map[string]struct{}{
	"transport": {}, "lodging": {}, "food": {}, "attraction": {}, "shopping": {},
	"entertainment": {}, "ticket": {}, "gift": {}, "medical": {}, "other": {},
	"flight": {}, "train": {}, "car": {}, "fuel": {}, "parking": {}, "ship": {}, "bike": {},
	"coffee": {}, "drink": {}, "alcohol": {}, "dessert": {},
	"photo": {}, "nature": {}, "beach": {}, "camping": {}, "amusement": {}, "art": {},
	"movie": {}, "music": {}, "game": {}, "sport": {}, "spa": {},
	"clothing": {}, "beauty": {}, "phone": {}, "baby": {}, "pet": {},
	"pharmacy": {}, "insurance": {}, "tips": {},
}

// CategoryFields 是分类可局部更新的字段名，用于 changed_fields 与字段级合并。
var CategoryFields = []string{"name", "icon", "sort_order"}

// EntityTypeLedger 是账目的同步实体类型。
const EntityTypeLedger = "ledger_entry"

// LedgerKind 是账目类型：支出或退款；金额一律为正，方向由类型决定，创建后不可改。
type LedgerKind string

const (
	KindExpense LedgerKind = "expense"
	KindRefund  LedgerKind = "refund"
)

// Valid 判断是否为已知类型。
func (k LedgerKind) Valid() bool { return k == KindExpense || k == KindRefund }

// LedgerResource 是账目的规范资源，也是同步日志与快照中的表示（接口设计 3.5 LedgerEntry）。
// CurrencyCode 由旅行派生、只读；AttachmentAssetIDs 是票据图片资产 ID，按显示顺序排列。
// PayerMemberID 是付款人（退款为收款人）；Splits 是服务端按 SplitMode 计算的各参与人份额；
// SplitCount = 参与人数，PersonalAmount = 「我」的份额，均为派生只读字段。
type LedgerResource struct {
	ID                 uuid.UUID     `json:"id"`
	TripID             uuid.UUID     `json:"trip_id"`
	Kind               LedgerKind    `json:"kind"`
	Amount             string        `json:"amount"`
	SplitCount         int32         `json:"split_count"`
	PersonalAmount     string        `json:"personal_amount"`
	PayerMemberID      uuid.UUID     `json:"payer_member_id"`
	SplitMode          SplitMode     `json:"split_mode"`
	Splits             []LedgerSplit `json:"splits"`
	CurrencyCode       string        `json:"currency_code"`
	CategoryID         uuid.UUID     `json:"category_id"`
	OccurredOn         types.Date    `json:"occurred_on"`
	Notes              string        `json:"notes"`
	RefundedEntryID    *uuid.UUID    `json:"refunded_entry_id"`
	AttachmentAssetIDs []uuid.UUID   `json:"attachment_asset_ids"`
	Version            types.Version `json:"version"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
	DeletedAt          *time.Time    `json:"deleted_at"`
}

// LedgerFields 是账目可局部更新的字段名，用于 changed_fields 与字段级合并；kind 与 currency_code 不可改。
var LedgerFields = []string{"amount", "payer_member_id", "split_mode", "splits", "category_id", "occurred_on", "notes", "refunded_entry_id", "attachment_asset_ids"}

// MaxLedgerAttachments 是每条账目的票据上限（接口设计 3.5）。
const MaxLedgerAttachments = 10

// LedgerFilters 是列表查询条件（接口设计 3.9 LedgerFilters）；原始字符串由服务校验。
type LedgerFilters struct {
	HasRefunds      *bool
	SplitMode       string
	DateFrom        string
	DateTo          string
	CategoryID      *uuid.UUID
	Kind            string
	RefundedEntryID *uuid.UUID
	Limit           int
	Cursor          string
}

// StatisticsFilters 是统计查询条件（接口设计 3.9 StatisticsFilters）。
type StatisticsFilters struct {
	SplitMode   string
	DateFrom    string
	DateTo      string
	CategoryID  *uuid.UUID
	DailyLimit  int
	DailyCursor string
}

// StatisticsScope 是统计实际采用的筛选范围（接口设计 3.8）。
type StatisticsScope struct {
	SplitMode  *SplitMode  `json:"split_mode"`
	DateFrom   *types.Date `json:"date_from"`
	DateTo     *types.Date `json:"date_to"`
	CategoryID *uuid.UUID  `json:"category_id"`
}

// Totals 是支出、退款、净额与条数；净额 = 支出 − 退款，独立退款可使其为负。
type Totals struct {
	ExpenseAmount string `json:"expense_amount"`
	RefundAmount  string `json:"refund_amount"`
	NetAmount     string `json:"net_amount"`
	EntryCount    int64  `json:"entry_count"`
}

// TripBudget 是整趟旅行的预算对比；无总预算时剩余与超支为 nil。
type TripBudget struct {
	BudgetAmount    *string `json:"budget_amount"`
	TripNetAmount   string  `json:"trip_net_amount"`
	RemainingAmount *string `json:"remaining_amount"`
	OverspentAmount *string `json:"overspent_amount"`
}

// CategoryTotals 是单个分类的汇总；Share 仅供展示，ratio_available=false 时为 nil。
type CategoryTotals struct {
	CategoryID            uuid.UUID `json:"category_id"`
	Name                  string    `json:"name"`
	Icon                  *string   `json:"icon"`
	ExpenseAmount         string    `json:"expense_amount"`
	RefundAmount          string    `json:"refund_amount"`
	NetAmount             string    `json:"net_amount"`
	Share                 *float64  `json:"share"`
	TripCategoryNetAmount string    `json:"trip_category_net_amount"`
}

// DailyTotals 是某一天的汇总。
type DailyTotals struct {
	Date          types.Date `json:"date"`
	ExpenseAmount string     `json:"expense_amount"`
	RefundAmount  string     `json:"refund_amount"`
	NetAmount     string     `json:"net_amount"`
}

// Statistics 是旅行开支统计响应（接口设计 3.8 TripStatistics）。
type Statistics struct {
	Scope          StatisticsScope          `json:"scope"`
	CurrencyCode   string                   `json:"currency_code"`
	FilteredTotals Totals                   `json:"filtered_totals"`
	TripBudget     TripBudget               `json:"trip_budget"`
	ByCategory     []CategoryTotals         `json:"by_category"`
	RatioAvailable bool                     `json:"ratio_available"`
	Daily          paging.Page[DailyTotals] `json:"daily"`
}
