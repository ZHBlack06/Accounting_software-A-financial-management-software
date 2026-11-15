// Package money 提供分 ↔ 元的展示与解析（仅边界使用，业务层一律 int64 分）。
package money

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var yuanReplacer = strings.NewReplacer("¥", "", "￥", "", ",", "", " ", "")

// FormatCents 把分格式化为 '¥1,234.56'，负数形如 '-¥90.00'。
func FormatCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	yuan := cents / 100
	frac := cents % 100
	// 千分位
	s := strconv.FormatInt(yuan, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return fmt.Sprintf("%s¥%s.%02d", sign, b.String(), frac)
}

// ParseYuan 解析 '¥1,234.56' / '100.00' / '--' 等为分。
// 空串与 '--' 返回错误；只在解析边界使用一次 float。
func ParseYuan(s string) (int64, error) {
	s = strings.TrimSpace(yuanReplacer.Replace(s))
	if s == "" || s == "--" {
		return 0, fmt.Errorf("金额为空")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("金额格式非法: %s", s)
	}
	return int64(math.Round(v * 100)), nil
}

// fullWidthReplacer 把全角数字/标点归一为半角（表格里常见）。
var fullWidthReplacer = strings.NewReplacer(
	"０", "0", "１", "1", "２", "2", "３", "3", "４", "4",
	"５", "5", "６", "6", "７", "7", "８", "8", "９", "9",
	"．", ".", "，", ",", "（", "(", "）", ")", "－", "-", "￥", "¥",
	"\u00a0", " ", "　", " ",
)

// amountDecor 去掉币种与装饰字符（分隔符、单位、国际货币代码）。
var amountDecor = strings.NewReplacer(
	"¥", "", "$", "", "元", "", "圆", "", "RMB", "", "CNY", "", "人民币", "",
	",", "", " ", "",
)

// ErrAmountEmpty 金额为空（调用方视为无效行）。
var ErrAmountEmpty = fmt.Errorf("金额为空")

// ParseAmountCents 字符串定点解析金额为分，全程不经过 float（§4.3）。
//
// 支持：'¥1,234.56'、'(1,234.56)'（会计负数）、'100-'（借方后缀负号）、
// '1 234.5'、'.5'、'863.1999999999999'（第 3 位 ≥5 进位 → 86320 分）。
// 非法输入返回错误，调用方按无效行处理。
func ParseAmountCents(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	s = fullWidthReplacer.Replace(s)
	neg := false

	// 会计记法：首尾成对括号 = 负数
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") && len(s) > 1 {
		neg = !neg
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	// 前缀负号
	if strings.HasPrefix(s, "-") {
		neg = !neg
		s = strings.TrimSpace(strings.TrimPrefix(s, "-"))
	}
	// 后缀负号（借方记法 '100-'）
	if strings.HasSuffix(s, "-") {
		neg = !neg
		s = strings.TrimSpace(strings.TrimSuffix(s, "-"))
	}

	s = strings.TrimSpace(amountDecor.Replace(s))
	switch s {
	case "", "-", "--", "—", "/", "N/A", "n/a":
		return 0, ErrAmountEmpty
	}

	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return 0, fmt.Errorf("金额格式非法: %s", raw)
	}
	intPart := parts[0]
	fracPart := ""
	if len(parts) == 2 {
		fracPart = parts[1]
	}
	if !allDigits(intPart) || !allDigits(fracPart) {
		return 0, fmt.Errorf("金额格式非法: %s", raw)
	}
	if intPart == "" && fracPart == "" {
		return 0, ErrAmountEmpty
	}

	// 整数位（允许为空，'.5' → 0）
	var yuan int64
	if intPart != "" {
		v, err := strconv.ParseInt(intPart, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("金额超出范围: %s", raw)
		}
		yuan = v
	}
	// 小数位：右补零到 2 位；第 3 位 ≥ '5' 则进位（字符串比较，不碰 float）
	roundUp := false
	if len(fracPart) > 2 {
		roundUp = fracPart[2] >= '5'
		fracPart = fracPart[:2]
	}
	for len(fracPart) < 2 {
		fracPart += "0"
	}
	var frac int64
	if fracPart != "" {
		v, err := strconv.ParseInt(fracPart, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("金额格式非法: %s", raw)
		}
		frac = v
	}
	cents := yuan*100 + frac
	if roundUp {
		cents++
	}
	if neg {
		cents = -cents
	}
	return cents, nil
}

// allDigits 判断是否全为数字（空串视为合法）。
func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
