package sheetparse

import (
	"fmt"
	"strings"

	"ledger/internal/pkg/timeutil"
)

// RowParser 按列映射把一行原始单元格解析为 ParsedRow。
// 列名 → 列下标映射在构造时建立一次，逐行解析 O(映射字段数)。
type RowParser struct {
	columns  []string
	index    map[string]int
	plan     *SheetPlan
	date1904 bool
}

// NewRowParser 构造行解析器。columns 为表头行（已 TrimSpace）。
func NewRowParser(columns []string, plan *SheetPlan, date1904 bool) *RowParser {
	idx := make(map[string]int, len(columns))
	for i, c := range columns {
		name := strings.TrimSpace(c)
		if name == "" {
			continue
		}
		if _, exists := idx[name]; !exists {
			idx[name] = i
		}
	}
	p := &RowParser{columns: columns, index: idx, plan: plan, date1904: date1904}
	if p.plan.DirectionRule.Mode == "" {
		if p.plan.Mapping.Direction != "" {
			p.plan.DirectionRule.Mode = "column"
		} else {
			p.plan.DirectionRule.Mode = "form_default"
		}
	}
	p.plan.DirectionRule.Normalize()
	if p.plan.TimePrecision != PrecisionDay {
		p.plan.TimePrecision = PrecisionSecond
	}
	return p
}

// ColumnIndex 返回列名对应的下标（不存在返回 -1）。
func (p *RowParser) ColumnIndex(name string) int {
	if name == "" {
		return -1
	}
	if i, ok := p.index[strings.TrimSpace(name)]; ok {
		return i
	}
	return -1
}

// cell 取指定列的单元格原文（列不存在返回空串）。
func (p *RowParser) cell(row []string, column string) string {
	i := p.ColumnIndex(column)
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

// rawMap 收集已映射列的原始值（行级 raw，最多 11 个字段，内存可控）。
func (p *RowParser) rawMap(row []string) map[string]string {
	m := make(map[string]string, len(MappingTargets))
	for _, target := range MappingTargets {
		col := p.plan.Mapping.Column(target)
		if col == "" {
			continue
		}
		if v := p.cell(row, col); v != "" {
			m[col] = v
		}
	}
	return m
}

// Parse 解析一行。返回 (parsed, nil) 或 (nil, rowError)，绝不返回空指针双重 nil。
func (p *RowParser) Parse(row []string, rowNo int, fileSHA1 string) (*ParsedRow, *RowError) {
	raw := p.rawMap(row)
	pr := &ParsedRow{
		FileIndex: p.plan.FileIndex, FileID: p.plan.FileID,
		SheetIndex: p.plan.SheetIndex, SheetName: p.plan.SheetName,
		RowNo:  rowNo,
		RowKey: fmt.Sprintf("%s:%d:%d", p.plan.FileID, p.plan.SheetIndex, rowNo),
		Raw:    raw, TagNames: []string{}, Warnings: []string{},
	}

	// ---- 日期 ----
	dateCol := p.plan.Mapping.Date
	dateCell := p.cell(row, dateCol)
	if dateCol == "" {
		dateCol = p.plan.Mapping.Month
		dateCell = p.cell(row, dateCol)
		if dateCell == "" {
			return nil, &RowError{RowNo: rowNo, Column: "日期", Value: "", Reason: "日期为空"}
		}
	}
	if dateCell == "" {
		return nil, &RowError{RowNo: rowNo, Column: dateCol, Value: "", Reason: "日期为空"}
	}
	unix, hasTime, warn, err := ParseDate(dateCell, p.date1904)
	if err != nil {
		return nil, &RowError{RowNo: rowNo, Column: dateCol, Value: dateCell, Reason: "日期无法解析"}
	}
	switch warn {
	case "only_year_month":
		pr.Warnings = append(pr.Warnings, "仅到年月，已按当月 1 日处理")
	case "typo_month":
		pr.Warnings = append(pr.Warnings, "月份写法有误（如 2018月11月），已按 2018年11月 处理")
	}
	pr.TimeUnix = unix
	pr.HasTime = hasTime
	pr.LocalDate = timeutil.LocalDate(unix)

	// ---- 金额 ----
	amountCol := p.plan.Mapping.Amount
	amountCell := p.cell(row, amountCol)
	if amountCell == "" {
		return nil, &RowError{RowNo: rowNo, Column: amountCol, Value: "", Reason: "金额为空"}
	}
	signed, err := ParseAmount(amountCell)
	if err != nil {
		return nil, &RowError{RowNo: rowNo, Column: amountCol, Value: amountCell, Reason: "金额无法解析"}
	}
	if signed == 0 {
		return nil, &RowError{RowNo: rowNo, Column: amountCol, Value: amountCell, Reason: "金额为 0"}
	}

	// ---- 分类名 ----
	pr.CategoryName = p.cell(row, p.plan.Mapping.Category)
	pr.SubCategoryName = p.cell(row, p.plan.Mapping.SubCategory)

	// ---- 方向 ----
	dirCell := p.cell(row, p.plan.Mapping.Direction)
	typ, derr := p.plan.DirectionRule.Infer(dirCell, signed, pr.CategoryName)
	if derr != nil {
		return nil, &RowError{RowNo: rowNo, Column: p.plan.Mapping.Direction,
			Value: dirCell, Reason: "无法推断收支方向"}
	}
	pr.Type = typ
	amount := signed
	if amount < 0 {
		amount = -amount
	}
	pr.AmountCents = amount

	// ---- 时间精度（§4.1）----
	// 铁律：**源数据没有时分秒就一定是 day**（时间由系统按当日平均分布生成），
	// 绝不能因为「用户把全局精度设为 second」就把它标成 second——那会让用户以为
	// 那串 04:00:00 是从源里读出来的。
	// 反之源里有时间时：plan 设为 day → 同样降级为 day 并参与分布（用户显式要按天看）；
	// plan 为 second → 原样保留源时间，标记 second。
	switch {
	case !hasTime:
		pr.TimePrecision = PrecisionDay
		pr.HasTime = false
	case p.plan.TimePrecision == PrecisionDay:
		pr.TimePrecision = PrecisionDay
		pr.HasTime = false // 丢弃源时间，交由 applyDayDistribution 按当日重排
	default:
		pr.TimePrecision = PrecisionSecond
	}

	// ---- 标签 / 备注 / 商户 ----
	pr.TagNames = SplitTags(p.cell(row, p.plan.Mapping.Tag))
	if len(pr.TagNames) > 10 {
		pr.TagNames = pr.TagNames[:10]
		pr.Warnings = append(pr.Warnings, "标签超过 10 个，已截断")
	}
	// ---- 备注 / 商户（两个列只有其一时各自落到自己的字段）----
	pr.MerchantText = p.cell(row, p.plan.Mapping.Merchant)
	pr.Remark = p.cell(row, p.plan.Mapping.Remark)

	// ---- 去重键 ----
	if src := p.cell(row, p.plan.Mapping.ExternalID); src != "" {
		pr.ExternalID = "imp:" + src
	} else {
		pr.ExternalID = RowExternalID(fileSHA1, p.plan.SheetIndex, rowNo)
	}
	return pr, nil
}

// SplitCategory 按 category_split（"/" 或 ">"）拆分两级分类名，只切一次。
// 返回 (L1, L2)；未启用拆分或名称不含分隔符时 L2 为空。
func SplitCategory(name, sep string) (string, string) {
	name = strings.TrimSpace(name)
	if sep == "" || name == "" {
		return name, ""
	}
	if i := strings.Index(name, sep); i >= 0 {
		return strings.TrimSpace(name[:i]), strings.TrimSpace(name[i+len(sep):])
	}
	return name, ""
}
