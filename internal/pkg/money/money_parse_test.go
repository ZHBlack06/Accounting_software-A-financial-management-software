package money

import "testing"

// TestParseAmountCents 覆盖 §4.3 的全部验证用例。
func TestParseAmountCents(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"¥1,234.56", 123456},
		{"(1,234.56)", -123456},
		{"100-", -10000},
		{"863.1999999999999", 86320},
		{"1 234.5", 123450},
		{".5", 50},
		{"272.16", 27216},
		{"5", 500},
		{"1.5", 150},
		{"0.01", 1},
		{"¥ 0.10", 10},
		{"１２３．４５", 12345}, // 全角
		{"（１，２３４．５６）", -123456}, // 全角括号 + 千分位
		{"-1,234.56", -123456},
		{"1.005", 101}, // 第 3 位 5 进位
		{"1.004", 100},
		{"RMB 1,000.00", 100000},
		{"12.34元", 1234},
	}
	for _, c := range cases {
		got, err := ParseAmountCents(c.in)
		if err != nil {
			t.Fatalf("ParseAmountCents(%q) 返回错误: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseAmountCents(%q) = %d，want %d", c.in, got, c.want)
		}
	}
}

// TestParseAmountCentsInvalid 无效输入必须报错（调用方视为无效行）。
func TestParseAmountCentsInvalid(t *testing.T) {
	for _, in := range []string{"", "  ", "--", "—", "/", "N/A", "总计", "1.2.3", "abc", "-"} {
		if v, err := ParseAmountCents(in); err == nil {
			t.Errorf("ParseAmountCents(%q) 期望错误，实得 %d", in, v)
		}
	}
}

// TestParseAmountCentsNoFloatError 复现 float 方案的 1 分误差场景。
func TestParseAmountCentsNoFloatError(t *testing.T) {
	// 863.1999999999999 * 100 在 float 下 = 86319.99999999999 → round → 86320 也可能对，
	// 但 4.35 这类值 float 会得 434.99999999999994 → trunc 434；此处断言定点结果稳定。
	got, err := ParseAmountCents("4.35")
	if err != nil || got != 435 {
		t.Fatalf("ParseAmountCents(4.35) = %d, %v；want 435", got, err)
	}
	if legacy, _ := ParseYuan("4.35"); legacy != 435 {
		t.Logf("legacy ParseYuan(4.35) = %d", legacy)
	}
}
