// Package metadata 提供随程序版本固定的枚举与限额，对应 GET /metadata（接口设计 §6）。
// 这些值同时约束服务端校验：客户端不能使用这里没有的枚举值。
package metadata

// Currency 是支持的币种及其小数位。
type Currency struct {
	Code       string
	MinorUnits int
}

// UploadLimits 是文件上传限制。
type UploadLimits struct {
	ImageMaxBytes   int64
	PDFMaxBytes     int64
	ImageMediaTypes []string
	PDFMediaTypes   []string
}

// MapSettings 是地图供应商与坐标系。
type MapSettings struct {
	Provider         string
	CoordinateSystem string
}

// Metadata 是 GET /metadata 的业务模型；HTTP 层负责转换为契约模型。
type Metadata struct {
	ProtocolVersion       int
	DefaultCurrencyCode   string
	Currencies            []Currency
	LedgerKinds           []string
	ExpenseCategoryIcons  []string
	PackingCategories     []string
	PackingStatuses       []string
	PackingLibraryVersion string
	ItineraryKinds        []string
	ItineraryStatuses     []string
	ReservationKinds      []string
	UploadLimits          UploadLimits
	Map                   MapSettings
}

const (
	// ProtocolVersion 是同步协议版本。
	ProtocolVersion = 1
	// DefaultCurrencyCode 是创建旅行时未指定币种的默认值。
	DefaultCurrencyCode = "CNY"
	// PackingLibraryVersion 是内置物品库版本；物品库内容变更时递增。
	PackingLibraryVersion = "1"

	mib = 1024 * 1024
)

// currencies 按接口设计 §6 的初始币种集合排列：JPY、KRW、VND 为 0 位，KWD、BHD 为 3 位，其余 2 位。
var currencies = []Currency{
	{"CNY", 2}, {"USD", 2}, {"EUR", 2}, {"GBP", 2}, {"JPY", 0}, {"HKD", 2}, {"MOP", 2},
	{"TWD", 2}, {"KRW", 0}, {"SGD", 2}, {"THB", 2}, {"MYR", 2}, {"IDR", 2}, {"VND", 0},
	{"AUD", 2}, {"CAD", 2}, {"CHF", 2}, {"NZD", 2}, {"AED", 2}, {"KWD", 3}, {"BHD", 3},
}

var minorUnitsByCode = func() map[string]int {
	m := make(map[string]int, len(currencies))
	for _, c := range currencies {
		m[c.Code] = c.MinorUnits
	}
	return m
}()

// Current 返回当前程序的元数据。每次调用返回新的切片副本，调用方可以安全修改。
func Current() Metadata {
	return Metadata{
		ProtocolVersion:       ProtocolVersion,
		DefaultCurrencyCode:   DefaultCurrencyCode,
		Currencies:            append([]Currency(nil), currencies...),
		LedgerKinds:           []string{"expense", "refund"},
		ExpenseCategoryIcons:  expenseCategoryIcons(),
		PackingCategories:     []string{"documents", "electronics", "clothing", "daily", "food", "medicine", "other"},
		PackingStatuses:       []string{"pending", "ready", "packed"},
		PackingLibraryVersion: PackingLibraryVersion,
		ItineraryKinds:        []string{"attraction", "transport", "lodging", "dining", "other"},
		ItineraryStatuses:     []string{"pending", "completed", "skipped"},
		ReservationKinds:      []string{"transport", "lodging", "attraction", "other"},
		UploadLimits: UploadLimits{
			ImageMaxBytes:   20 * mib,
			PDFMaxBytes:     50 * mib,
			ImageMediaTypes: []string{"image/jpeg", "image/png", "image/webp"},
			PDFMediaTypes:   []string{"application/pdf"},
		},
		Map: MapSettings{Provider: "amap", CoordinateSystem: "GCJ-02"},
	}
}

// MinorUnits 返回币种的小数位；未支持的币种返回 ok=false。
func MinorUnits(code string) (units int, ok bool) {
	units, ok = minorUnitsByCode[code]
	return units, ok
}

// expenseCategoryIcons 是账单分类可选图标键，顺序即客户端选择器的展示顺序，与 finance.CategoryIcons 保持一致。
func expenseCategoryIcons() []string {
	return []string{
		"transport", "lodging", "food", "attraction", "shopping", "entertainment", "ticket", "gift", "medical", "other",
		"flight", "train", "car", "fuel", "parking", "ship", "bike",
		"coffee", "drink", "alcohol", "dessert",
		"photo", "nature", "beach", "camping", "amusement", "art", "movie", "music", "game", "sport", "spa",
		"clothing", "beauty", "phone", "baby", "pet", "pharmacy", "insurance", "tips",
	}
}
