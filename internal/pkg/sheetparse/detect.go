package sheetparse

import (
	"fmt"
	"regexp"
	"strings"
)

// Detected 单张工作表的识别结果（probe 阶段）。
type Detected struct {
	HeaderRow   int
	Candidates  []int
	Columns     []string
	Mapping     map[string]string
	Direction   string
	Skip        bool
	SkipReason  string
	DateHasTime bool
	Warning     string
}

const (
	// scanHeaderRows 只在每张表前 10 行里找表头（§4.7）。
	scanHeaderRows = 10
	// scanValueRows 值形状校验使用的样本行数。
	scanValueRows = 20
)

// headerKeywords 表头打分关键词（命中 +1）。
var headerKeywords = []string{
	"日期", "时间", "金额", "分类", "备注", "账户", "标签", "收支",
	"收入", "支出", "商户", "说明", "项目", "月份", "单号", "摘要", "用途", "数额",
}

var (
	reTotals = regexp.MustCompile(`合计|总计|小计|汇总`)
	rePivot  = regexp.MustCompile(`占比|%|排行|环比|同比|合计|总计|小计`)
)

// dateNameKeywords / amountNameKeywords 用于判定「表头有无日期类/金额类列」。
var (
	dateNameKeywords   = []string{"日期", "时间", "月份", "年月", "date", "time"}
	amountNameKeywords = []string{"金额", "发生额", "数额", "价格", "收入", "支出", "amount"}
)

// fieldSpec 单个映射目标的同义词与值形状校验方式。
type fieldSpec struct {
	target   string
	exact    []string
	contains []string
	shape    string // "" | "date" | "amount"
}

var fieldSpecs = []fieldSpec{
	{TargetDate, []string{"日期", "交易日期", "交易时间", "发生时间", "记账日期", "date", "time"},
		[]string{"日期", "时间"}, "date"},
	{TargetMonth, []string{"月份", "年月", "month"}, []string{"月份", "年月"}, ""},
	{TargetAmount, []string{"金额", "发生额", "交易金额", "数额", "价格", "金额(元)", "金额（元）", "amount"},
		[]string{"金额", "数额", "价格"}, "amount"},
	{TargetDirection, []string{"收支", "收/支", "方向", "借贷", "收支类型"}, []string{"收支"}, ""},
	{TargetCategory, []string{"分类", "类别", "项目分类", "大类"}, []string{"分类", "类别"}, ""},
	{TargetSubCategory, []string{"子分类", "二级分类", "小类"}, []string{"子分类"}, ""},
	{TargetAccount, []string{"账户", "账号", "卡号", "支付方式", "account"}, []string{"账户", "账号"}, ""},
	{TargetTag, []string{"标签", "tag", "tags"}, []string{"标签"}, ""},
	{TargetRemark, []string{"备注", "说明", "描述", "摘要", "remark", "memo"}, []string{"备注", "说明"}, ""},
	{TargetMerchant, []string{"商户", "商家", "交易对方", "对方", "收款方", "merchant"},
		[]string{"商户", "商家", "对方"}, ""},
	{TargetExternalID, []string{"单号", "流水号", "交易单号", "订单号", "外部单号", "external_id"},
		[]string{"单号", "流水号"}, ""},
}

// Detect 识别表头行、列名、建议映射、汇总表判定与日期是否带时间。
// rows 为工作表前若干行（按原始顺序，第 0 行为表头候选起点）。
func Detect(rows [][]string, sheetName string) Detected {
	d := Detected{Mapping: map[string]string{}, Candidates: []int{}}
	d.HeaderRow, d.Candidates, d.Warning = detectHeaderRow(rows)
	if d.HeaderRow < 1 {
		d.HeaderRow = 1
	}
	if d.HeaderRow > len(rows) {
		d.HeaderRow = len(rows)
	}
	if d.HeaderRow < 1 {
		d.HeaderRow = 1
	}
	d.Columns = columnsAt(rows, d.HeaderRow)
	d.Mapping = suggestMapping(rows, d.HeaderRow, d.Columns)
	d.DateHasTime = dateHasColumnTime(rows, d.HeaderRow, d.Columns, d.Mapping[TargetDate])
	d.Skip, d.SkipReason = detectSkip(rows, d.HeaderRow, d.Columns, d.Candidates)
	d.Direction = suggestDirection(sheetName)
	return d
}

// detectHeaderRow 表头候选行打分：取最高分（同分取最小行号）。
func detectHeaderRow(rows [][]string) (int, []int, string) {
	limit := len(rows)
	if limit > scanHeaderRows {
		limit = scanHeaderRows
	}
	if limit == 0 {
		return 1, []int{}, "未识别到表头，请手动指定表头行"
	}
	bestRow, bestScore := 0, 0
	var candidates []int
	for i := 0; i < limit; i++ {
		score := scoreHeaderRow(rows[i])
		if score >= 2 {
			candidates = append(candidates, i+1)
		}
		if score > bestScore {
			bestScore, bestRow = score, i+1
		}
	}
	if bestRow == 0 {
		return 1, candidates, "未识别到表头，请手动指定表头行"
	}
	return bestRow, candidates, ""
}

// scoreHeaderRow 单行表头打分（§4.7）。
func scoreHeaderRow(row []string) int {
	score := 0
	nonEmpty := 0
	parseable := 0
	for _, c := range row {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		nonEmpty++
		for _, kw := range headerKeywords {
			if strings.Contains(c, kw) {
				score++
				break
			}
		}
		if _, _, _, err := ParseDate(c, false); err == nil {
			parseable++
		} else if v, err := ParseAmount(c); err == nil && v != 0 {
			parseable++
		}
	}
	if nonEmpty >= 2 {
		score++
	}
	// 数据行降权：≥50% 单元格可解析为金额或日期
	if nonEmpty > 0 && parseable*2 >= nonEmpty {
		score -= 2
	}
	return score
}

// columnsAt 取第 rowNo 行（1 基）的列名，去掉尾部空列。
func columnsAt(rows [][]string, rowNo int) []string {
	if rowNo < 1 || rowNo > len(rows) {
		return []string{}
	}
	row := rows[rowNo-1]
	last := -1
	for i, c := range row {
		if strings.TrimSpace(c) != "" {
			last = i
		}
	}
	if last < 0 {
		return []string{}
	}
	out := make([]string, last+1)
	for i := 0; i <= last; i++ {
		out[i] = strings.TrimSpace(row[i])
	}
	return out
}

// valueSamples 返回表头之后最多 scanValueRows 行的可见列值。
func valueSamples(rows [][]string, headerRow int, columns []string) [][]string {
	start := headerRow // rows 为 0 基，表头在 index headerRow-1
	if start >= len(rows) {
		return nil
	}
	end := start + scanValueRows
	if end > len(rows) {
		end = len(rows)
	}
	out := make([][]string, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, rows[i])
	}
	return out
}

// suggestMapping 每目标字段独立打分，列不可重复占用（§4.7）。
func suggestMapping(rows [][]string, headerRow int, columns []string) map[string]string {
	m := map[string]string{}
	samples := valueSamples(rows, headerRow, columns)
	used := map[string]bool{}
	for _, spec := range fieldSpecs {
		bestCol := ""
		bestScore := 0.0
		for ci, col := range columns {
			if col == "" || used[col] {
				continue
			}
			if sc := scoreColumn(spec, col, ci, samples); sc > bestScore {
				bestScore, bestCol = sc, col
			}
		}
		if bestCol != "" && bestScore >= 2.0 {
			m[spec.target] = bestCol
			used[bestCol] = true
		}
	}
	// date 与 month 同时命中时保留 date（更精确），month 仅在只有年月列时使用
	if m[TargetDate] != "" {
		delete(m, TargetMonth)
	}
	return m
}

// scoreColumn 列打分：3*完全等于同义词 + 2*包含同义词 + 值形状（0..2）。
func scoreColumn(spec fieldSpec, col string, colIdx int, samples [][]string) float64 {
	name := strings.ToLower(strings.TrimSpace(col))
	score := 0.0
	for _, kw := range spec.exact {
		if name == strings.ToLower(kw) {
			score += 3
			break
		}
	}
	if score == 0 {
		for _, kw := range spec.contains {
			if strings.Contains(name, strings.ToLower(kw)) {
				score += 2
				break
			}
		}
	}
	if spec.shape == "" || len(samples) == 0 {
		return score
	}
	ok, total := 0, 0
	for _, row := range samples {
		if colIdx >= len(row) {
			continue
		}
		v := strings.TrimSpace(row[colIdx])
		if v == "" {
			continue
		}
		total++
		switch spec.shape {
		case "date":
			if _, _, _, err := ParseDate(v, false); err == nil {
				ok++
			}
		case "amount":
			if cents, err := ParseAmount(v); err == nil && cents != 0 {
				ok++
			}
		}
	}
	if total > 0 {
		score += 2 * float64(ok) / float64(total)
	}
	return score
}

// dateHasColumnTime 判断日期列样本中是否含时分秒。
func dateHasColumnTime(rows [][]string, headerRow int, columns []string, dateCol string) bool {
	if dateCol == "" {
		return false
	}
	idx := -1
	for i, c := range columns {
		if c == dateCol {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	for _, row := range valueSamples(rows, headerRow, columns) {
		if idx >= len(row) {
			continue
		}
		v := strings.TrimSpace(row[idx])
		if v == "" {
			continue
		}
		if _, hasTime, _, err := ParseDate(v, false); err == nil && hasTime {
			return true
		}
	}
	return false
}

// detectSkip 汇总/透视表判定（§4.7 四条规则，任一命中即跳过）。
func detectSkip(rows [][]string, headerRow int, columns []string, candidates []int) (bool, string) {
	var reasons []string
	// 1) 前 30 行首列出现 合计|总计|小计|汇总
	limit := len(rows)
	if limit > 30 {
		limit = 30
	}
	for i := 0; i < limit; i++ {
		if len(rows[i]) == 0 {
			continue
		}
		if reTotals.MatchString(strings.TrimSpace(rows[i][0])) {
			reasons = append(reasons, "首列出现「合计/总计」")
			break
		}
	}
	// 2) 表头既无日期类列也无金额类列
	hasDate, hasAmount := false, false
	for _, c := range columns {
		lower := strings.ToLower(strings.TrimSpace(c))
		for _, kw := range dateNameKeywords {
			if strings.Contains(lower, kw) {
				hasDate = true
				break
			}
		}
		for _, kw := range amountNameKeywords {
			if strings.Contains(lower, kw) {
				hasAmount = true
				break
			}
		}
	}
	if !hasDate && !hasAmount {
		reasons = append(reasons, "表头无日期列也无金额列")
	}
	// 3) 连续两行都被判为表头（两行表头透视表）
	for i := 1; i < len(candidates); i++ {
		if candidates[i] == candidates[i-1]+1 {
			reasons = append(reasons, "连续两行为表头（透视表）")
			break
		}
	}
	// 4) 列名含 占比|%|排行|环比|同比|合计|总计|小计
	for _, c := range columns {
		if rePivot.MatchString(c) {
			reasons = append(reasons, "列名含「占比/合计/排行」等汇总字段")
			break
		}
	}
	if len(reasons) == 0 {
		return false, ""
	}
	return true, "疑似汇总/透视表：" + strings.Join(reasons, "；")
}

// suggestDirection 依据表名给出默认方向（「收入」表 → income，其余 expense）。
func suggestDirection(sheetName string) string {
	name := strings.TrimSpace(sheetName)
	if strings.Contains(name, "收入") || strings.Contains(strings.ToLower(name), "income") {
		return "income"
	}
	return "expense"
}

// FormatSkipReason 便于调用方拼接 warnings（保留中文口径）。
func FormatSkipReason(sheetName, reason string) string {
	if reason == "" {
		return ""
	}
	return fmt.Sprintf("「%s」%s，已建议不导入", sheetName, reason)
}
