package sheetparse

import (
	"testing"

	"ledger/internal/pkg/timeutil"
)

// TestParseDateSerial Excel 序列号解析（§4.2 第 1 步）。
func TestParseDateSerial(t *testing.T) {
	cases := []struct {
		in       string
		wantDate string
		hasTime  bool
	}{
		{"43383", "2018-10-10", false},
		{"43383.5", "2018-10-10", true},
		{"1", "1900-01-01", false},
		{"73415", "2100-12-31", false},
	}
	for _, c := range cases {
		unix, hasTime, warn, err := ParseDate(c.in, false)
		if err != nil {
			t.Fatalf("ParseDate(%q) 错误: %v", c.in, err)
		}
		if got := timeutil.LocalDate(unix); got != c.wantDate {
			t.Errorf("ParseDate(%q) = %s，want %s", c.in, got, c.wantDate)
		}
		if hasTime != c.hasTime {
			t.Errorf("ParseDate(%q) hasTime = %v，want %v", c.in, hasTime, c.hasTime)
		}
		if warn != "" {
			t.Errorf("ParseDate(%q) warn = %q，want 空", c.in, warn)
		}
	}
	if _, _, _, err := ParseDate("43383.5", false); err != nil {
		t.Fatalf("带小数序列号解析失败: %v", err)
	}
	// 43383.5 → 12:00:00
	unix, _, _, _ := ParseDate("43383.5", false)
	if got := timeutil.FormatLocal(unix); got != "2018-10-10 12:00:00" {
		t.Errorf("序列号小数时间 = %s，want 2018-10-10 12:00:00", got)
	}
}

// TestParseDateSerial1904 1904 系统基准。
func TestParseDateSerial1904(t *testing.T) {
	unix, _, _, err := ParseDate("1", true)
	if err != nil {
		t.Fatalf("1904 序列号解析失败: %v", err)
	}
	if got := timeutil.LocalDate(unix); got != "1904-01-02" {
		t.Errorf("1904 序列号 1 = %s，want 1904-01-02", got)
	}
}

// TestParseDateSerial60 Excel 虚构的 1900-02-29 必须报错。
func TestParseDateSerial60(t *testing.T) {
	if _, _, _, err := ParseDate("60", false); err == nil {
		t.Fatal("序列号 60（1900-02-29）应当报错")
	}
}

// TestParseDateText 文本日期白名单。
func TestParseDateText(t *testing.T) {
	cases := []struct {
		in       string
		wantDate string
		hasTime  bool
		warn     string
	}{
		{"2018-10-10 09:00:00", "2018-10-10", true, ""},
		{"2018-10-10 09:00", "2018-10-10", true, ""},
		{"2018-10-10", "2018-10-10", false, ""},
		{"2018-10-10T09:00:00", "2018-10-10", true, ""},
		{"2018/10/5 09:00", "2018-10-05", true, ""},
		{"2018/10/5", "2018-10-05", false, ""},
		{"2018.10.5", "2018-10-05", false, ""},
		{"2018年10月5日 09:00:00", "2018-10-05", true, ""},
		{"2018年10月5日", "2018-10-05", false, ""},
		{"2018年10月10日（周三）", "2018-10-10", false, ""},
		{"2018年10月10日 星期三", "2018-10-10", false, ""},
		{"2018-10-10 9:0:0", "2018-10-10", true, ""},
		{"2018年10月", "2018-10-01", false, "only_year_month"},
		{"2018月11月", "2018-11-01", false, "typo_month"},
		{"10/15/2018", "2018-10-15", false, ""},
		{"２０１８年１０月０５日", "2018-10-05", false, ""},
	}
	for _, c := range cases {
		unix, hasTime, warn, err := ParseDate(c.in, false)
		if err != nil {
			t.Fatalf("ParseDate(%q) 错误: %v", c.in, err)
		}
		if got := timeutil.LocalDate(unix); got != c.wantDate {
			t.Errorf("ParseDate(%q) = %s，want %s", c.in, got, c.wantDate)
		}
		if hasTime != c.hasTime {
			t.Errorf("ParseDate(%q) hasTime = %v，want %v", c.in, hasTime, c.hasTime)
		}
		if warn != c.warn {
			t.Errorf("ParseDate(%q) warn = %q，want %q", c.in, warn, c.warn)
		}
	}
}

// TestParseDateUnparsable 无法解析必须报错，不能静默成零值。
func TestParseDateUnparsable(t *testing.T) {
	for _, in := range []string{"", "  ", "11-17-19", "总计", "abc", "999999"} {
		if _, _, _, err := ParseDate(in, false); err == nil {
			t.Errorf("ParseDate(%q) 期望错误", in)
		}
	}
}

// TestParseDirectionPriority 方向推断优先级（§4.4）。
func TestParseDirectionPriority(t *testing.T) {
	rule := DirectionRule{Mode: "column"}
	if got, err := rule.Infer("收入", 100, ""); err != nil || got != "income" {
		t.Errorf("direction 列「收入」= %v, %v；want income", got, err)
	}
	if got, err := rule.Infer("支出", 100, ""); err != nil || got != "expense" {
		t.Errorf("direction 列「支出」= %v, %v；want expense", got, err)
	}
	// 2) 负数视为支出（优先级高于 form_default）
	fd := DirectionRule{Mode: "form_default", Default: "income"}
	if got, err := fd.Infer("", -100, ""); err != nil || got != "expense" {
		t.Errorf("负金额 = %v, %v；want expense", got, err)
	}
	// 3) form_default
	if got, err := fd.Infer("", 100, ""); err != nil || got != "income" {
		t.Errorf("form_default/income = %v, %v；want income", got, err)
	}
	// 4) by_category
	bc := DirectionRule{Mode: "by_category"}
	if got, err := bc.Infer("", 100, "收入"); err != nil || got != "income" {
		t.Errorf("by_category 收入 = %v, %v；want income", got, err)
	}
	if got, err := bc.Infer("", 100, "餐饮"); err != nil || got != "expense" {
		t.Errorf("by_category 餐饮 = %v, %v；want expense", got, err)
	}
	// 5) 无法确定
	columnOnly := DirectionRule{Mode: "column"}
	if _, err := columnOnly.Infer("/", 100, ""); err == nil {
		t.Error("column 模式遇到 '/' 且金额非负应报错")
	}
	// negative_is_expense=false 时负数不自动判支出
	f := false
	off := DirectionRule{Mode: "form_default", Default: "income", NegativeIsExpense: &f}
	if got, err := off.Infer("", -100, ""); err != nil || got != "income" {
		t.Errorf("negative_is_expense=false = %v, %v；want income", got, err)
	}
}

// TestSplitTags 标签多值拆分。
func TestSplitTags(t *testing.T) {
	got := SplitTags("报销, 出差；旅行|家庭")
	want := []string{"报销", "出差", "旅行", "家庭"}
	if len(got) != len(want) {
		t.Fatalf("SplitTags = %v，want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SplitTags[%d] = %q，want %q", i, got[i], want[i])
		}
	}
	if v := SplitTags("   "); len(v) != 0 {
		t.Errorf("空白标签应为空，实得 %v", v)
	}
}

// TestSplitCategory 两级分类拆分。
func TestSplitCategory(t *testing.T) {
	if l1, l2 := SplitCategory("餐饮/午饭", "/"); l1 != "餐饮" || l2 != "午饭" {
		t.Errorf("SplitCategory = %q,%q", l1, l2)
	}
	if l1, l2 := SplitCategory("餐饮", ""); l1 != "餐饮" || l2 != "" {
		t.Errorf("未启用拆分应原样返回: %q,%q", l1, l2)
	}
}
