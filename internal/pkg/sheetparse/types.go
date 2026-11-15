// Package sheetparse 通用表格（xlsx / csv）解析内核：纯函数、无数据库、无网络。
//
// 职责：
//  1. 列出工作表及其表头候选行、列名、样本行（Probe）；
//  2. 按用户映射把一行原始单元格解析成 ParsedRow（ParseRow）；
//  3. 同日无时间记录的确定性时间分布（DistributeDayTimes）。
//
// 设计依据：docs/ARCHITECTURE-V12.md §4.1–§4.7。
package sheetparse

// 文件类型。
const (
	KindXLSX = "xlsx"
	KindCSV  = "csv"
)

// 映射目标字段名（与 §2.3 契约一致）。
const (
	TargetDate        = "date"
	TargetMonth       = "month"
	TargetAmount      = "amount"
	TargetDirection   = "direction"
	TargetCategory    = "category"
	TargetSubCategory = "sub_category"
	TargetAccount     = "account"
	TargetTag         = "tag"
	TargetRemark      = "remark"
	TargetMerchant    = "merchant"
	TargetExternalID  = "external_id"
)

// MappingTargets 映射目标的固定顺序（用于建议映射的去重分配）。
var MappingTargets = []string{
	TargetDate, TargetMonth, TargetAmount, TargetDirection, TargetCategory,
	TargetSubCategory, TargetAccount, TargetTag, TargetRemark, TargetMerchant, TargetExternalID,
}

// Mapping 列映射：值为列名，空字符串表示未映射（JSON 中为 null）。
type Mapping struct {
	Date        string `json:"date"`
	Month       string `json:"month"`
	Amount      string `json:"amount"`
	Direction   string `json:"direction"`
	Category    string `json:"category"`
	SubCategory string `json:"sub_category"`
	Account     string `json:"account"`
	Tag         string `json:"tag"`
	Remark      string `json:"remark"`
	Merchant    string `json:"merchant"`
	ExternalID  string `json:"external_id"`
}

// Column 返回某映射目标对应的列名（未映射返回空串）。
func (m *Mapping) Column(target string) string {
	switch target {
	case TargetDate:
		return m.Date
	case TargetMonth:
		return m.Month
	case TargetAmount:
		return m.Amount
	case TargetDirection:
		return m.Direction
	case TargetCategory:
		return m.Category
	case TargetSubCategory:
		return m.SubCategory
	case TargetAccount:
		return m.Account
	case TargetTag:
		return m.Tag
	case TargetRemark:
		return m.Remark
	case TargetMerchant:
		return m.Merchant
	case TargetExternalID:
		return m.ExternalID
	}
	return ""
}

// DirectionRule 收支方向推断规则（§4.4）。
type DirectionRule struct {
	Mode                string   `json:"mode"` // column | form_default | by_category
	Default             string   `json:"default"`
	IncomeValues        []string `json:"income_values"`
	ExpenseValues       []string `json:"expense_values"`
	NegativeIsExpense   *bool    `json:"negative_is_expense"`
	IncomeCategoryNames []string `json:"income_category_names"`
}

// SheetPlan 单张工作表的解析计划（由 preview 请求给出并落盘，commit 复用）。
type SheetPlan struct {
	FileIndex     int           `json:"file_index"`
	FileID        string        `json:"file_id"`
	SheetIndex    int           `json:"sheet_index"`
	SheetName     string        `json:"sheet_name"`
	HeaderRow     int           `json:"header_row"`
	Skip          bool          `json:"skip"`
	Mapping       Mapping       `json:"mapping"`
	DirectionRule DirectionRule `json:"direction_rule"`
	CategorySplit string        `json:"category_split"`
	TimePrecision string        `json:"time_precision"`
}

// ParsedRow 单行解析结果（未做同日时间分布，分布由调用方按 local_date 分组完成）。
type ParsedRow struct {
	FileIndex       int               `json:"-"`
	FileID          string            `json:"-"`
	SheetIndex      int               `json:"-"`
	SheetName       string            `json:"-"`
	RowNo           int               `json:"row_no"`
	RowKey          string            `json:"row_key"`
	Raw             map[string]string `json:"raw"`
	TimeUnix        int64             `json:"-"`
	LocalDate       string            `json:"local_date"`
	HasTime         bool              `json:"-"`
	TimePrecision   string            `json:"time_precision"`
	Type            string            `json:"transaction_type"`
	AmountCents     int64             `json:"amount_cents"`
	CategoryName    string            `json:"category_name"`
	SubCategoryName string            `json:"sub_category_name"`
	TagNames        []string          `json:"tag_names"`
	MerchantText    string            `json:"merchant_text"`
	Remark          string            `json:"remark"`
	ExternalID      string            `json:"external_id"`
	Warnings        []string          `json:"warnings"`
}

// RowError 单行解析错误（预览可见、可单行修正，绝不静默丢行）。
type RowError struct {
	RowNo  int    `json:"row_no"`
	Column string `json:"column"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// FileMeta 单文件 probe 结果。
type FileMeta struct {
	FileName string      `json:"file_name"`
	Size     int64       `json:"file_size"`
	SHA1     string      `json:"sha1"`
	Kind     string      `json:"kind"`
	Encoding string      `json:"encoding"`
	Date1904 bool        `json:"-"`
	Sheets   []SheetMeta `json:"sheets"`
}

// SheetMeta 单张工作表 probe 结果。
type SheetMeta struct {
	SheetIndex          int               `json:"sheet_index"`
	SheetName           string            `json:"sheet_name"`
	RowCount            int               `json:"row_count"`
	ColCount            int               `json:"col_count"`
	HeaderRow           int               `json:"header_row"`
	HeaderRowCandidates []int             `json:"header_row_candidates"`
	Columns             []string          `json:"columns"`
	SampleRows          [][]string        `json:"sample_rows"`
	SuggestedMapping    map[string]string `json:"suggested_mapping"`
	SuggestedDirection  string            `json:"suggested_direction"`
	SuggestedSkip       bool              `json:"suggested_skip"`
	SkipReason          string            `json:"skip_reason"`
	DateHasTime         bool              `json:"date_has_time"`
	Warning             string            `json:"warning"`
}

// 时间精度取值。
const (
	PrecisionSecond = "second"
	PrecisionDay    = "day"
)
