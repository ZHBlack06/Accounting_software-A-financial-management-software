// Package csvparse 微信/支付宝账单 CSV 解析（§6.9 确定性规则）。
package csvparse

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"ledger/internal/pkg/money"
	"ledger/internal/pkg/timeutil"
)

// Parsed 单行解析结果。
type Parsed struct {
	TimeUnix            int64  `json:"-"`
	TimeStr             string `json:"transaction_time"`
	Type                string `json:"transaction_type"` // expense|income
	AmountCents         int64  `json:"amount_cents"`
	MerchantText        string `json:"merchant_text"`
	Remark              string `json:"remark"`
	ExternalID          string `json:"external_id"`
	CounterpartyName    string `json:"counterparty_name"`
	CounterpartyAccount string `json:"counterparty_account"`
	Currency            string `json:"currency"`
}

// Row 一行原始记录 + 解析结果（Parsed 为 nil 表示跳过）。
type Row struct {
	RowNo      int               `json:"row_no"`
	Raw        map[string]string `json:"raw"`
	Parsed     *Parsed           `json:"parsed"`
	SkipReason string            `json:"skip_reason,omitempty"`
	Warnings   []string          `json:"warnings"`
}

// File 解析后的账单文件。
type File struct {
	Source   string   `json:"source"`
	Encoding string   `json:"encoding"`
	Columns  []string `json:"columns"`
	Mapping  map[string]string `json:"mapping"`
	Rows     []Row    `json:"rows"`
}

// col 按候选列名取单元格（列名去空格）。
func col(rec []string, columns []string, names ...string) string {
	for _, name := range names {
		for i, c := range columns {
			if strings.TrimSpace(c) == name && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
		}
	}
	return ""
}

// hasCol 判断表头是否包含任一候选列名。
func hasCol(columns []string, names ...string) bool {
	for _, name := range names {
		for _, c := range columns {
			if strings.TrimSpace(c) == name {
				return true
			}
		}
	}
	return false
}

// parseAmount 解析金额为分（¥1,234.56 → 123456）。
func parseAmount(s string) (int64, error) {
	return money.ParseYuan(s)
}

// parseTime 解析 'YYYY-MM-DD HH:mm:ss' 为 UTC 秒。
func parseTime(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("时间为空")
	}
	return timeutil.ParseLocal(s)
}

// Fingerprint 无 external_id 时的行指纹（§6.9）：sha1(localDate|amount|account|merchant|type)[:16]。
func Fingerprint(date string, amountCents int64, accountID, merchant, typ string) string {
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%s|%s|%s", date, amountCents, accountID, merchant, typ)))
	return hex.EncodeToString(h[:])[:16]
}

// startOfDayLocal 便于测试。
func startOfDayLocal(t time.Time) time.Time {
	y, m, d := t.In(timeutil.Local()).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, timeutil.Local())
}
