package csvparse

import (
	"bytes"
	"encoding/csv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"ledger/internal/pkg/apierr"
)

// 探测顺序（§6.9）：
// 1. BOM/UTF-8 → 否则 GBK 转 UTF-8
// 2. 定位表头行（含"交易时间"且含金额列）
// 3. profile：微信（交易对方+商品+金额(元)）/ 支付宝（对方账号/商品说明/交易订单号/收入金额+支出金额）
// Parse 解析整份账单；无法识别返回 422 UNSUPPORTED_CSV（details.detected_headers）。
func Parse(data []byte) (*File, error) {
	text, encoding := decode(data)
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}

	headerIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "交易时间") || strings.Contains(line, "发生时间") {
			if strings.Contains(line, "金额") || (strings.Contains(line, "收入") && strings.Contains(line, "支出")) {
				headerIdx = i
				break
			}
		}
	}
	if headerIdx < 0 {
		return nil, apierr.New(apierr.CodeUnsupportedCSV, "无法识别账单格式，请确认是微信或支付宝导出的 CSV").
			WithDetails(map[string]any{"detected_headers": firstLines(lines, 5)})
	}

	reader := csv.NewReader(strings.NewReader(strings.Join(lines[headerIdx:], "\n")))
	reader.FieldsPerRecord = -1 // 账单行字段数可能不一致（尾部空列省略）
	records, err := reader.ReadAll()
	if err != nil {
		return nil, apierr.New(apierr.CodeUnsupportedCSV, "CSV 解析失败: "+err.Error()).
			WithDetails(map[string]any{"detected_headers": firstLines(lines, 5)})
	}
	if len(records) < 2 {
		return nil, apierr.New(apierr.CodeUnsupportedCSV, "账单内容为空").
			WithDetails(map[string]any{"detected_headers": firstLines(lines, 5)})
	}
	columns := make([]string, len(records[0]))
	for i, c := range records[0] {
		columns[i] = strings.TrimSpace(c)
	}
	colSet := strings.Join(columns, "|")

	f := &File{Encoding: encoding, Columns: columns, Mapping: map[string]string{}}
	switch {
	case strings.Contains(colSet, "收入金额") && strings.Contains(colSet, "支出金额"),
		strings.Contains(colSet, "对方账号"),
		strings.Contains(colSet, "商品说明"),
		strings.Contains(colSet, "交易订单号"),
		strings.Contains(colSet, "商品名称"):
		f.Source = "alipay"
	case hasCol(columns, "商品", "商品名称") && hasCol(columns, "交易对方"),
		strings.Contains(colSet, "金额（元）"), strings.Contains(colSet, "金额(元)"):
		f.Source = "wechat"
	default:
		return nil, apierr.New(apierr.CodeUnsupportedCSV, "无法识别账单格式（表头不匹配微信/支付宝）").
			WithDetails(map[string]any{"detected_headers": firstLines(lines, 5)})
	}

	body := records[1:]
	if f.Source == "wechat" {
		f.Rows, f.Mapping = parseWeChat(body, columns, headerIdx)
	} else {
		f.Rows, f.Mapping = parseAlipay(body, columns, headerIdx)
	}
	if len(f.Rows) == 0 {
		return nil, apierr.New(apierr.CodeUnsupportedCSV, "账单没有有效数据行").
			WithDetails(map[string]any{"detected_headers": firstLines(lines, 5)})
	}
	return f, nil
}

// DecodeText 导出 BOM/UTF-8/GBK 探测（供 sheetparse 的通用 CSV 读取复用）。
// 返回解码后的文本与编码名（"utf-8" | "gbk"）。
func DecodeText(data []byte) (string, string) { return decode(data) }

// decode BOM/UTF-8/GBK 探测。
func decode(data []byte) (string, string) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(data) {
		return string(data), "utf-8"
	}
	// 尝试 GBK 转 UTF-8（支付宝导出）
	if out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), data); err == nil {
		return string(out), "gbk"
	}
	return string(data), "utf-8"
}

// firstLines 供 422 details 使用（前 n 个非空行，截断 120 字符）。
func firstLines(lines []string, n int) []string {
	var out []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if utf8.RuneCountInString(l) > 120 {
			l = string([]rune(l)[:120])
		}
		out = append(out, l)
		if len(out) >= n {
			break
		}
	}
	return out
}

// rawMap 表头 + 记录 → map。
func rawMap(columns, rec []string) map[string]string {
	m := map[string]string{}
	for i, c := range columns {
		if i < len(rec) {
			m[c] = strings.TrimSpace(rec[i])
		}
	}
	return m
}
