package sheetparse

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ledger/internal/pkg/money"
	"ledger/internal/pkg/timeutil"
)

// 日期文本归一化用的正则（§4.2）。
var (
	reWeekdayParen = regexp.MustCompile(`[（(]\s*(周|星期)[一二三四五六日天]\s*[)）]\s*$`)
	reWeekdayTail  = regexp.MustCompile(`\s*(周|星期)[一二三四五六日天]\s*$`)
	reTimeLoose    = regexp.MustCompile(`(\d{1,2}):(\d{1,2})(:(\d{1,2}))?`)
	reISOT         = regexp.MustCompile(`^(\d{4}-\d{1,2}-\d{1,2})[Tt](\d.*)$`)
	reTypoMonth    = regexp.MustCompile(`^(\d{4})月(\d{1,2})月$`)
	reSlashMDY     = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})$`)
	reDigitsOnly   = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
)

// dateHalfWidth 全角数字/标点 → 半角（表格里偶见）。
var dateHalfWidth = strings.NewReplacer(
	"０", "0", "１", "1", "２", "2", "３", "3", "４", "4",
	"５", "5", "６", "6", "７", "7", "８", "8", "９", "9",
	"－", "-", "／", "/", "．", ".", "：", ":", "　", " ",
)

// 文本日期 layout 白名单（顺序即优先级，§4.2 第 2 步）。
type dateLayout struct {
	layout  string
	hasTime bool
}

var dateLayouts = []dateLayout{
	{"2006-01-02 15:04:05", true},
	{"2006-01-02 15:04", true},
	{"2006-1-2 15:04:05", true},
	{"2006-1-2 15:04", true},
	{"2006-01-02", false},
	{"2006-1-2", false},
	{"2006/1/2 15:04:05", true},
	{"2006/1/2 15:04", true},
	{"2006/1/2", false},
	{"2006.1.2", false},
	{"2006年1月2日 15:04:05", true},
	{"2006年1月2日 15:04", true},
	{"2006年1月2日", false},
	{"2006年01月02日", false},
}

// 仅年月的 layout（按当月 1 日处理，返回 only_year_month 警告）。
var yearMonthLayouts = []string{"2006年1月", "2006年01月", "2006-01", "2006/1"}

// Excel 序列号允许范围：1900-01-01 .. 2100-12-31。
const (
	serialMin = 1
	serialMax = 73415
)

// ParseDate 解析日期/日期时间为 UTC 秒。
//
// 返回：unix（UTC 秒，Asia/Shanghai 语义）、hasTime（源是否自带时分秒）、
// warn（"only_year_month" / "typo_month"，无需提示时为空串）、err。
//
// 支持：Excel 序列号（含小数时间）、ISO/斜杠/点分/中文年月日、仅年月、错字 `2018月11月`、M/D/Y。
// 无法解析时返回错误（调用方记为 date_unparsable，绝不静默丢行）。
func ParseDate(raw string, date1904 bool) (unix int64, hasTime bool, warn string, err error) {
	s := normalizeDateText(raw)
	if s == "" {
		return 0, false, "", fmt.Errorf("日期为空")
	}
	// 第 1 步：纯数值 → Excel 序列号
	if reDigitsOnly.MatchString(s) {
		if v, perr := strconv.ParseFloat(s, 64); perr == nil && v >= serialMin && v <= serialMax {
			return serialToUnix(v, date1904)
		}
	}
	// 第 2 步：文本 layout 白名单
	for _, l := range dateLayouts {
		if t, perr := time.ParseInLocation(l.layout, s, timeutil.Local()); perr == nil {
			return t.Unix(), l.hasTime, "", nil
		}
	}
	// 仅年月（2018年10月 / 2018-10）
	for _, l := range yearMonthLayouts {
		if strings.Contains(l, "月") {
			if t, perr := time.ParseInLocation(l, s, timeutil.Local()); perr == nil {
				return t.Unix(), false, "only_year_month", nil
			}
			continue
		}
		if t, perr := time.ParseInLocation(l, s, timeutil.Local()); perr == nil {
			return t.Unix(), false, "only_year_month", nil
		}
	}
	// 错字归一：2018月11月 → 2018年11月
	if m := reTypoMonth.FindStringSubmatch(s); m != nil {
		fixed := m[1] + "年" + m[2] + "月"
		for _, l := range yearMonthLayouts[:2] {
			if t, perr := time.ParseInLocation(l, fixed, timeutil.Local()); perr == nil {
				return t.Unix(), false, "typo_month", nil
			}
		}
	}
	// M/D/Y：仅当第 2 段 > 12 且第 1 段 ≤ 12
	if m := reSlashMDY.FindStringSubmatch(s); m != nil {
		mo, _ := strconv.Atoi(m[1])
		day, _ := strconv.Atoi(m[2])
		yr, _ := strconv.Atoi(m[3])
		if day > 12 && mo <= 12 {
			t := time.Date(yr, time.Month(mo), day, 0, 0, 0, 0, timeutil.Local())
			return t.Unix(), false, "", nil
		}
	}
	return 0, false, "", fmt.Errorf("date_unparsable: %s", raw)
}

// normalizeDateText 归一化日期文本：全角转半角、去星期后缀、T 分隔符、非补零时间补零。
func normalizeDateText(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = dateHalfWidth.Replace(s)
	s = strings.TrimSpace(s)
	s = reWeekdayParen.ReplaceAllString(s, "")
	s = reWeekdayTail.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if m := reISOT.FindStringSubmatch(s); m != nil {
		s = m[1] + " " + m[2]
	}
	// 时间部分补零：9:0:0 → 09:00:00
	if strings.Contains(s, ":") {
		s = reTimeLoose.ReplaceAllStringFunc(s, func(t string) string {
			m := reTimeLoose.FindStringSubmatch(t)
			h, _ := strconv.Atoi(m[1])
			mi, _ := strconv.Atoi(m[2])
			if m[3] == "" {
				return fmt.Sprintf("%02d:%02d", h, mi)
			}
			sec, _ := strconv.Atoi(m[4])
			return fmt.Sprintf("%02d:%02d:%02d", h, mi, sec)
		})
	}
	return s
}

// serialToUnix Excel 序列号 → UTC 秒。1900 系统基准 1899-12-30，
// 1904 系统基准 1904-01-01；序列号 60（Excel 虚构的 1900-02-29）报错。
func serialToUnix(v float64, date1904 bool) (int64, bool, string, error) {
	days := int(v)
	frac := v - float64(days)
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, timeutil.Local())
	if date1904 {
		base = time.Date(1904, 1, 1, 0, 0, 0, 0, timeutil.Local())
	}
	if !date1904 && days == 60 {
		return 0, false, "", fmt.Errorf("date_invalid: Excel 虚构日期 1900-02-29")
	}
	// Excel 1900 系统的闰年 bug：序列号 ≤59 时基准要回退一天（1 = 1900-01-01），
	// 序列号 ≥61 时用 1899-12-30（补上虚构的 1900-02-29）。
	if !date1904 && days < 61 {
		days++
	}
	sec := int(math.Round(frac * 86400))
	if sec >= 86400 {
		days++
		sec = 0
	}
	t := base.AddDate(0, 0, days).Add(time.Duration(sec) * time.Second)
	return t.Unix(), sec != 0, "", nil
}

// Normalize 补齐收支方向规则的默认值（§4.4 默认词表）。
func (r *DirectionRule) Normalize() {
	if r.Default == "" {
		r.Default = "expense"
	}
	if len(r.IncomeValues) == 0 {
		r.IncomeValues = []string{"收入", "收", "入账", "贷", "credit"}
	}
	if len(r.ExpenseValues) == 0 {
		r.ExpenseValues = []string{"支出", "支", "出账", "借", "debit"}
	}
	if len(r.IncomeCategoryNames) == 0 {
		r.IncomeCategoryNames = []string{"收入"}
	}
}

// NegativeIsExpenseEnabled 负金额是否视为支出（默认 true）。
func (r *DirectionRule) NegativeIsExpenseEnabled() bool {
	if r.NegativeIsExpense == nil {
		return true
	}
	return *r.NegativeIsExpense
}

// Infer 按 §4.4 的优先级推断收支方向；无法确定时返回错误（调用方记 direction_unresolved）。
func (r *DirectionRule) Infer(directionCell string, amountCents int64, categoryName string) (string, error) {
	rule := *r
	rule.Normalize()
	cell := strings.TrimSpace(directionCell)
	if cell != "" {
		if matchAny(cell, rule.IncomeValues) {
			return "income", nil
		}
		if matchAny(cell, rule.ExpenseValues) {
			return "expense", nil
		}
	}
	if rule.NegativeIsExpenseEnabled() && amountCents < 0 {
		return "expense", nil
	}
	switch rule.Mode {
	case "form_default":
		if rule.Default == "income" {
			return "income", nil
		}
		return "expense", nil
	case "by_category":
		cat := strings.TrimSpace(categoryName)
		for _, n := range rule.IncomeCategoryNames {
			if cat == strings.TrimSpace(n) {
				return "income", nil
			}
		}
		return "expense", nil
	case "", "column":
		// 映射了方向列但无法识别，且金额非负 → 无法确定
		return "", fmt.Errorf("direction_unresolved")
	}
	return "", fmt.Errorf("direction_unresolved")
}

// matchAny 精确命中优先，其次包含匹配（兼容「收入/支出」「收入合计」这类写法）。
func matchAny(cell string, values []string) bool {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if cell == v {
			return true
		}
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if len([]rune(v)) < 1 || v == "/" || v == "-" || v == "中性" {
			continue
		}
		if strings.Contains(cell, v) {
			return true
		}
	}
	return false
}

// ParseAmount 金额单元格 → 分（负数保留符号，供方向推断与取绝对值使用）。
func ParseAmount(raw string) (int64, error) { return money.ParseAmountCents(raw) }

// SplitTags 标签多值拆分（§4.5）：[, ， 、 | ; ；]，每段 TrimSpace 后去空。
func SplitTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', '、', '|', ';', '；':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// NormalizeName 分类/标签名称处理的唯一入口：只做 strings.TrimSpace。
//
// 严禁：全半角归一、大小写折叠、繁简转换、去内部空格、同义词合并。
// 因此「其他」与「其它」是两个不同分类（§4.5 铁律，禁止后人「优化」）。
func NormalizeName(s string) string { return strings.TrimSpace(s) }
