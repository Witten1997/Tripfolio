package packing

// Library 是随程序发布的版本化内置物品库（接口设计 3.4 PackingLibrary）；不落库。
type Library struct {
	Version    string            `json:"version"`
	Categories []LibraryCategory `json:"categories"`
}

// LibraryCategory 是物品库中的一个分类及其物品。
type LibraryCategory struct {
	Category Category      `json:"category"`
	Name     string        `json:"name"`
	Items    []LibraryItem `json:"items"`
}

// LibraryItem 是物品库中的一件物品建议。
type LibraryItem struct {
	Name     string `json:"name"`
	Quantity int32  `json:"quantity"`
}

// LibraryVersion 是内置物品库版本，与 metadata.PackingLibraryVersion 一致；物品库内容变更时递增。
const LibraryVersion = "1"

// builtinLibrary 是按分类组织的内置物品库（需求 v0.3 §物资清单）。
var builtinLibrary = Library{
	Version: LibraryVersion,
	Categories: []LibraryCategory{
		{Category: CategoryDocuments, Name: "证件", Items: []LibraryItem{
			{"身份证", 1}, {"护照", 1}, {"港澳通行证", 1}, {"签证", 1}, {"学生证", 1},
			{"户口簿", 1}, {"驾驶证", 1}, {"银行卡", 1}, {"证件照", 2},
		}},
		{Category: CategoryElectronics, Name: "数码产品", Items: []LibraryItem{
			{"手机", 1}, {"充电器", 1}, {"充电宝", 1}, {"数据线", 2}, {"耳机", 1},
			{"相机", 1}, {"存储卡", 1}, {"转换插头", 1}, {"笔记本电脑", 1},
		}},
		{Category: CategoryClothing, Name: "服装", Items: []LibraryItem{
			{"上衣", 3}, {"裤子", 2}, {"内衣", 3}, {"袜子", 3}, {"外套", 1},
			{"睡衣", 1}, {"鞋子", 1}, {"帽子", 1}, {"雨伞", 1},
		}},
		{Category: CategoryDaily, Name: "生活用品", Items: []LibraryItem{
			{"牙刷", 1}, {"牙膏", 1}, {"毛巾", 1}, {"洗面奶", 1}, {"洗发水", 1},
			{"护肤品", 1}, {"防晒霜", 1}, {"梳子", 1}, {"纸巾", 1},
		}},
		{Category: CategoryFood, Name: "食品饮料", Items: []LibraryItem{
			{"零食", 1}, {"水", 1}, {"泡面", 1}, {"咖啡", 1}, {"口香糖", 1},
		}},
		{Category: CategoryMedicine, Name: "药品", Items: []LibraryItem{
			{"感冒药", 1}, {"退烧药", 1}, {"肠胃药", 1}, {"创可贴", 1}, {"晕车药", 1}, {"个人常用药", 1},
		}},
		{Category: CategoryOther, Name: "其他", Items: []LibraryItem{
			{"现金", 1}, {"眼镜", 1}, {"隐形眼镜", 1}, {"针线包", 1}, {"密封袋", 1},
		}},
	},
}

// BuiltinLibrary 返回内置物品库的深拷贝，调用方可安全修改。
func BuiltinLibrary() Library {
	out := Library{Version: builtinLibrary.Version, Categories: make([]LibraryCategory, len(builtinLibrary.Categories))}
	for i, c := range builtinLibrary.Categories {
		items := make([]LibraryItem, len(c.Items))
		copy(items, c.Items)
		out.Categories[i] = LibraryCategory{Category: c.Category, Name: c.Name, Items: items}
	}
	return out
}
