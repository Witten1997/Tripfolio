package finance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"tripfolio/server/internal/foundation/apperr"
)

const importSheet = "账单"
const referenceSheet = "分类与成员"
const metadataSheet = "_tripfolio"
const importFirstRow = 5

var importColumns = []string{"金额", "账单分类", "分摊模式", "实际日期", "备注", "分摊成员", "付款人"}

type workbookRow struct {
	Number int
	Cells  [7]string
}

type ledgerWorkbook struct {
	FileHash    string
	ContextHash string
	Currency    string
	Categories  map[string]uuid.UUID
	Members     map[string]uuid.UUID
	MemberOrder []uuid.UUID
	Rows        []workbookRow
	Errors      []LedgerImportError
}

func buildLedgerTemplate(accountID, tripID uuid.UUID, c importContext) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	var buildErr error
	check := func(err error) {
		if buildErr == nil {
			buildErr = err
		}
	}
	put := func(sheet, cell, value string) { check(f.SetCellStr(sheet, cell, value)) }
	check(f.SetSheetName("Sheet1", importSheet))
	_, err := f.NewSheet(referenceSheet)
	check(err)
	_, err = f.NewSheet(metadataSheet)
	check(err)
	put(importSheet, "A1", "批量导入支出账单")
	put(importSheet, "A2", "币种："+c.Trip.CurrencyCode+"。从第 5 行填写，最多 1000 笔；分类、模式、付款人可下拉选择，付款人留空默认为我。")
	for row := 1; row <= 3; row++ {
		check(f.MergeCell(importSheet, fmt.Sprintf("A%d", row), fmt.Sprintf("G%d", row)))
	}
	for i, name := range importColumns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 4)
		put(importSheet, cell, name)
	}
	put(referenceSheet, "A1", "账单分类")
	put(referenceSheet, "C1", "可用分摊成员（姓名）")
	put(referenceSheet, "D1", "成员编号")
	put(referenceSheet, "E1", "成员比例")
	put(referenceSheet, "G1", "分摊成员填写说明与导入规则")
	put(metadataSheet, "A1", "TripfolioLedgerImport")
	put(metadataSheet, "B1", "1")
	put(metadataSheet, "A2", accountID.String())
	put(metadataSheet, "B2", tripID.String())
	put(metadataSheet, "C2", c.Trip.CurrencyCode)
	put(metadataSheet, "D2", importContextDigest(c))
	for i, category := range c.Categories {
		put(referenceSheet, fmt.Sprintf("A%d", i+2), category.Name)
		put(metadataSheet, fmt.Sprintf("A%d", i+4), category.ID.String())
		put(metadataSheet, fmt.Sprintf("B%d", i+4), category.Name)
	}
	memberNames := map[string]bool{}
	for _, m := range c.Members {
		memberNames[strings.ToLower(m.Name)] = true
	}
	var exampleAliases, exampleMembers []string
	for i, m := range c.Members {
		alias := fmt.Sprintf("M%02d", i+1)
		for memberNames[strings.ToLower(alias)] {
			alias += "_"
		}
		if i < 2 {
			exampleAliases = append(exampleAliases, alias)
			exampleMembers = append(exampleMembers, alias+" = "+m.Name)
		}
		put(referenceSheet, fmt.Sprintf("C%d", i+2), m.Name)
		put(referenceSheet, fmt.Sprintf("D%d", i+2), alias)
		put(referenceSheet, fmt.Sprintf("E%d", i+2), m.SharePercent+"%")
		put(metadataSheet, fmt.Sprintf("D%d", i+4), m.ID.String())
		put(metadataSheet, fmt.Sprintf("E%d", i+4), m.Name)
		put(metadataSheet, fmt.Sprintf("F%d", i+4), alias)
	}
	memberExample := strings.Join(exampleAliases, ";")
	put(importSheet, "A3", fmt.Sprintf("分摊成员需手动填写：均摊或按比例时，填写姓名或编号，用分号分隔。编号填写示例：%s（直接填写，不加引号）。\n可填写「全部成员」；个人模式请留空。按比例沿用成员管理中设置的比例。\n本行程有 %d 位可用成员，完整姓名、编号及比例请查看底部「分类与成员」工作表的 C–E 列。", memberExample, len(c.Members)))
	rules := []string{
		fmt.Sprintf("本行程共 %d 位可用分摊成员，完整名单见左侧 C–E 列。", len(c.Members)),
		"均摊、按比例：在账单的「分摊成员」列手动填写姓名或编号，多人用分号 ; 分隔。",
		"编号填写示例：" + memberExample + "（直接填写，不加引号）。",
		"示例对应成员：" + strings.Join(exampleMembers, "；") + "。",
		"也可填写「全部成员」，指本模板列出的全部成员。姓名含分号时请使用成员编号。",
		"个人模式：分摊成员留空，付款人留空或选择本人。",
		"按比例使用导入预览时成员管理中设置的比例，按所选成员重新归一化。",
		"每一行是一笔支出；金额、分类、实际日期必填，备注可选。",
		"分摊模式留空默认为个人；付款人留空默认为本人。",
		"实际日期填写 YYYY-MM-DD，或使用 Excel 日期单元格。",
		"金额须大于 0，小数位按币种限制；备注最多 4000 字。",
		"上传后核对预览再确认；有错误时整批不导入。",
		"请勿修改表头、参考数据或模板信息；只导入账单工作表。",
		"不支持公式、退款和附件；请将公式结果粘贴为值。",
	}
	for i, text := range rules {
		put(referenceSheet, fmt.Sprintf("G%d", i+2), text)
	}
	referenceEnd := max(len(rules), len(c.Categories), len(c.Members)) + 1
	end := importFirstRow + MaxLedgerImportRows - 1
	for row := importFirstRow; row <= end; row++ {
		put(importSheet, fmt.Sprintf("C%d", row), "个人")
		check(f.SetRowHeight(importSheet, row, 24))
	}
	check(f.SetDefinedName(&excelize.DefinedName{Name: "BillCategories", RefersTo: fmt.Sprintf("'%s'!$A$2:$A$%d", referenceSheet, len(c.Categories)+1)}))
	check(f.SetDefinedName(&excelize.DefinedName{Name: "BillMembers", RefersTo: fmt.Sprintf("'%s'!$C$2:$C$%d", referenceSheet, len(c.Members)+1)}))
	for _, item := range []struct{ column, source, hint string }{
		{"B", "BillCategories", "请选择本账号的账单分类"},
		{"G", "BillMembers", "留空默认为我；个人账单只能由本人付款"},
	} {
		dv := excelize.NewDataValidation(true)
		dv.SetSqref(fmt.Sprintf("%s%d:%s%d", item.column, importFirstRow, item.column, end))
		dv.SetSqrefDropList(item.source)
		dv.SetInput("填写提示", item.hint)
		dv.SetError(excelize.DataValidationErrorStyleStop, "请选择有效值", item.hint)
		check(f.AddDataValidation(importSheet, dv))
	}
	dv := excelize.NewDataValidation(true)
	dv.SetSqref(fmt.Sprintf("C%d:C%d", importFirstRow, end))
	check(dv.SetDropList([]string{"个人", "均摊", "按比例"}))
	dv.SetInput("分摊模式", "留空默认为个人；按比例沿用成员管理中的比例")
	dv.SetError(excelize.DataValidationErrorStyleStop, "分摊模式不正确", "请选择个人、均摊或按比例")
	check(f.AddDataValidation(importSheet, dv))
	participants := excelize.NewDataValidation(true)
	participants.SetSqref(fmt.Sprintf("F%d:F%d", importFirstRow, end))
	participants.SetInput("分摊成员填写说明", "均摊或按比例：填写姓名或编号，用分号分隔，如 "+memberExample+"；也可填「全部成员」。个人模式留空。完整成员名单见「分类与成员」工作表 C–E 列。")
	check(f.AddDataValidation(importSheet, participants))
	header, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF", Size: 11}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"264653"}}, Alignment: &excelize.Alignment{Vertical: "center"}})
	check(err)
	body, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Size: 11, Color: "24343B"}, Alignment: &excelize.Alignment{Vertical: "top", WrapText: true}})
	check(err)
	title, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Size: 18, Color: "264653"}})
	check(err)
	check(f.SetCellStyle(importSheet, "A1", "G1", title))
	check(f.SetCellStyle(importSheet, "A2", fmt.Sprintf("G%d", end), body))
	check(f.SetCellStyle(importSheet, "A4", "G4", header))
	check(f.SetCellStyle(referenceSheet, "A1", "G1", header))
	check(f.SetCellStyle(referenceSheet, "A2", fmt.Sprintf("G%d", referenceEnd), body))
	units, err := ledgerMinorUnits(c.Trip.CurrencyCode)
	check(err)
	amountFormat := "0"
	if units > 0 {
		amountFormat += "." + strings.Repeat("0", units)
	}
	for _, item := range []struct{ column, format string }{{"A", amountFormat}, {"D", "yyyy-mm-dd"}, {"E", "@"}, {"F", "@"}} {
		style, err := f.NewStyle(&excelize.Style{CustomNumFmt: &item.format, Font: &excelize.Font{Size: 11}, Alignment: &excelize.Alignment{Vertical: "top", WrapText: true}})
		check(err)
		check(f.SetCellStyle(importSheet, item.column+"5", fmt.Sprintf("%s%d", item.column, end), style))
	}
	for i, width := range []float64{16, 24, 14, 18, 45, 36, 24} {
		col, _ := excelize.ColumnNumberToName(i + 1)
		check(f.SetColWidth(importSheet, col, col, width))
	}
	check(f.SetColWidth(referenceSheet, "A", "A", 30))
	check(f.SetColWidth(referenceSheet, "C", "C", 30))
	check(f.SetColWidth(referenceSheet, "D", "E", 15))
	check(f.SetColWidth(referenceSheet, "G", "G", 85))
	check(f.SetRowHeight(referenceSheet, 1, 26))
	for i := 0; i < referenceEnd-1; i++ {
		lines := 1
		if i < len(rules) {
			lines = max(lines, (utf8.RuneCountInString(rules[i])+39)/40)
		}
		if i < len(c.Categories) {
			lines = max(lines, (utf8.RuneCountInString(c.Categories[i].Name)+13)/14)
		}
		if i < len(c.Members) {
			lines = max(lines, (utf8.RuneCountInString(c.Members[i].Name)+13)/14)
		}
		check(f.SetRowHeight(referenceSheet, i+2, float64(max(26, lines*18))))
	}
	for row := 1; row <= 4; row++ {
		check(f.SetRowHeight(importSheet, row, 32))
	}
	check(f.SetRowHeight(importSheet, 3, 68))
	check(f.SetPanes(importSheet, &excelize.Panes{Freeze: true, YSplit: 4, TopLeftCell: "A5", ActivePane: "bottomLeft"}))
	check(f.SetPanes(referenceSheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}))
	check(f.SetSheetVisible(metadataSheet, false, true))
	check(f.SetSheetDimension(importSheet, fmt.Sprintf("A1:G%d", end)))
	check(f.SetSheetDimension(referenceSheet, fmt.Sprintf("A1:G%d", referenceEnd)))
	f.SetActiveSheet(0)
	if buildErr != nil {
		return nil, apperr.Internal(buildErr)
	}
	buffer, err := f.WriteToBuffer()
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return buffer.Bytes(), nil
}

func invalidWorkbook(message string) error { return apperr.BadRequest("INVALID_IMPORT_FILE", message) }

func parseLedgerWorkbook(file []byte, accountID, tripID uuid.UUID) (ledgerWorkbook, error) {
	doc := ledgerWorkbook{Categories: map[string]uuid.UUID{}, Members: map[string]uuid.UUID{}, MemberOrder: []uuid.UUID{}, Rows: []workbookRow{}, Errors: []LedgerImportError{}}
	if len(file) == 0 || len(file) > MaxLedgerImportBytes {
		return doc, invalidWorkbook("请上传不超过 5 MB 的 .xlsx 模板文件")
	}
	f, err := excelize.OpenReader(bytes.NewReader(file), excelize.Options{UnzipSizeLimit: 32 * 1024 * 1024, UnzipXMLSizeLimit: 8 * 1024 * 1024})
	if err != nil {
		return doc, invalidWorkbook("无法读取 Excel，请使用未加密的 .xlsx 模板文件")
	}
	defer f.Close()
	get := func(sheet, cell string) string {
		v, _ := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
		return v
	}
	if get(metadataSheet, "A1") != "TripfolioLedgerImport" || get(metadataSheet, "B1") != "1" {
		return doc, invalidWorkbook("模板信息缺失或版本不支持，请重新下载模板")
	}
	if get(metadataSheet, "A2") != accountID.String() || get(metadataSheet, "B2") != tripID.String() {
		return doc, invalidWorkbook("此模板不属于当前账号和行程，请重新下载模板")
	}
	doc.Currency, doc.ContextHash = get(metadataSheet, "C2"), get(metadataSheet, "D2")
	h := sha256.Sum256(file)
	doc.FileHash = hex.EncodeToString(h[:])
	meta, err := f.Rows(metadataSheet)
	if err != nil {
		return doc, invalidWorkbook("模板参考数据缺失")
	}
	defer meta.Close()
	aliases := map[string]uuid.UUID{}
	memberIDs := map[uuid.UUID]bool{}
	for number := 1; meta.Next(); number++ {
		if number > 10004 {
			return doc, invalidWorkbook("模板参考数据过多，请重新下载模板")
		}
		if number < 4 {
			continue
		}
		cells, e := meta.Columns(excelize.Options{RawCellValue: true})
		if e != nil {
			return doc, invalidWorkbook("模板参考数据无法读取")
		}
		value := func(i int) string {
			if i >= len(cells) {
				return ""
			}
			return cells[i]
		}
		if value(0) != "" {
			id, e := uuid.Parse(value(0))
			if e != nil || strings.TrimSpace(value(1)) == "" {
				return doc, invalidWorkbook("模板分类信息损坏")
			}
			key := strings.ToLower(strings.TrimSpace(value(1)))
			if _, exists := doc.Categories[key]; exists {
				return doc, invalidWorkbook("模板分类重复")
			}
			doc.Categories[key] = id
		}
		if value(3) != "" {
			id, e := uuid.Parse(value(3))
			if e != nil || strings.TrimSpace(value(4)) == "" || memberIDs[id] {
				return doc, invalidWorkbook("模板成员信息损坏或重复")
			}
			memberIDs[id] = true
			key := strings.ToLower(strings.TrimSpace(value(4)))
			if _, exists := doc.Members[key]; exists {
				return doc, invalidWorkbook("模板成员名称重复")
			}
			doc.Members[key] = id
			doc.MemberOrder = append(doc.MemberOrder, id)
			alias := strings.ToLower(value(5))
			if alias != "" {
				aliases[alias] = id
			}
		}
	}
	if err := meta.Error(); err != nil {
		return doc, invalidWorkbook("模板参考数据无法读取")
	}
	for alias, id := range aliases {
		if existing, ok := doc.Members[alias]; ok && existing != id {
			return doc, invalidWorkbook("模板成员编号与姓名冲突，请重新下载模板")
		}
		doc.Members[alias] = id
	}
	if len(doc.MemberOrder) == 0 || len(doc.MemberOrder) > 50 {
		return doc, invalidWorkbook("模板成员信息不完整")
	}
	for i, name := range importColumns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 4)
		if get(importSheet, cell) != name {
			return doc, invalidWorkbook("账单表头已修改，请使用原模板并保持列顺序")
		}
	}
	props, err := f.GetWorkbookProps()
	if err != nil {
		return doc, invalidWorkbook("无法读取模板日期设置")
	}
	date1904 := props.Date1904 != nil && *props.Date1904
	rows, err := f.Rows(importSheet)
	if err != nil {
		return doc, invalidWorkbook("找不到账单工作表")
	}
	defer rows.Close()
	for number := 1; rows.Next(); number++ {
		if number < importFirstRow {
			continue
		}
		if number >= importFirstRow+MaxLedgerImportRows {
			return doc, invalidWorkbook("单次最多导入 1000 笔，请填写在第 5–1004 行")
		}
		cells, e := rows.Columns(excelize.Options{RawCellValue: true})
		if e != nil {
			return doc, invalidWorkbook("无法读取账单内容")
		}
		raw := workbookRow{Number: number}
		populated := false
		for i, value := range cells {
			if i < 7 {
				raw.Cells[i] = strings.TrimSpace(value)
				if i == 4 {
					raw.Cells[i] = value
				}
			}
			if strings.TrimSpace(value) != "" && !(i == 2 && strings.TrimSpace(value) == "个人") {
				populated = true
			}
		}
		for i := range importColumns {
			cell, _ := excelize.CoordinatesToCellName(i+1, number)
			formula, e := f.GetCellFormula(importSheet, cell)
			if e != nil {
				return doc, invalidWorkbook("无法读取单元格")
			}
			if formula != "" {
				populated = true
				doc.Errors = append(doc.Errors, LedgerImportError{Row: number, Column: importColumns[i], Message: "不支持公式，请将公式结果粘贴为值"})
			}
		}
		if !populated {
			continue
		}
		for _, value := range cells[min(7, len(cells)):] {
			if strings.TrimSpace(value) != "" {
				doc.Errors = append(doc.Errors, LedgerImportError{Row: number, Column: "额外列", Message: "请勿在模板规定的七列之外填写数据"})
				break
			}
		}
		if serial, e := strconv.ParseFloat(raw.Cells[3], 64); e == nil && !math.IsNaN(serial) && !math.IsInf(serial, 0) {
			if serial > 0 && serial < 2958466 && (date1904 || math.Floor(serial) != 60) {
				date, e := excelize.ExcelDateToTime(serial, date1904)
				if e == nil {
					raw.Cells[3] = date.Format("2006-01-02")
				}
			}
		}
		doc.Rows = append(doc.Rows, raw)
	}
	if err := rows.Error(); err != nil {
		return doc, invalidWorkbook("账单工作表内容损坏")
	}
	return doc, nil
}
