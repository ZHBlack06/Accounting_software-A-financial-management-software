package csvparse

import "strings"

// parseWeChat 微信账单映射（§6.9 微信映射表）。
// 列：交易时间 / 交易类型 / 交易对方 / 商品 / 收/支 / 金额(元) / 支付方式 / 当前状态 / 交易单号 ...
// 收/支 为 "/"（中性交易）→ 跳过并计入 skipped。
func parseWeChat(body [][]string, columns []string, headerIdx int) ([]Row, map[string]string) {
	mapping := map[string]string{
		"transaction_time": "交易时间",
		"transaction_type": "收/支",
		"amount":           "金额(元)",
		"counterparty":     "交易对方",
		"remark":           "商品",
		"external_id":      "交易单号",
		"status":           "当前状态",
	}
	var rows []Row
	for i, rec := range body {
		raw := rawMap(columns, rec)
		if len(raw) == 0 {
			continue
		}
		row := Row{RowNo: i + 2, Raw: raw, Warnings: []string{}} // 2 = 表头行 + 数据偏移
		_ = headerIdx

		dir := col(rec, columns, "收/支")
		var typ string
		switch dir {
		case "支出":
			typ = "expense"
		case "收入":
			typ = "income"
		default:
			row.SkipReason = "收/支为 " + quoteOrEmpty(dir) + "（中性交易，不计收支）"
			rows = append(rows, row)
			continue
		}
		timeStr := col(rec, columns, "交易时间")
		t, err := parseTime(timeStr)
		if err != nil {
			row.SkipReason = "交易时间格式非法: " + timeStr
			rows = append(rows, row)
			continue
		}
		amountStr := col(rec, columns, "金额(元)", "金额（元）", "金额")
		amount, err := parseAmount(amountStr)
		if err != nil || amount <= 0 {
			row.SkipReason = "金额解析失败: " + amountStr
			rows = append(rows, row)
			continue
		}
		status := col(rec, columns, "当前状态")
		if strings.Contains(status, "已全额退款") || strings.Contains(status, "已退款") {
			row.Warnings = append(row.Warnings, "该笔已退款: "+status)
		}
		if txType := col(rec, columns, "交易类型"); strings.Contains(txType, "零钱提现") ||
			strings.Contains(txType, "信用卡还款") {
			row.Warnings = append(row.Warnings, "交易类型为「"+txType+"」，请确认是否应记为内部转账")
		}
		row.Parsed = &Parsed{
			TimeUnix:            t,
			TimeStr:             timeStr,
			Type:                typ,
			AmountCents:         amount,
			MerchantText:        col(rec, columns, "交易对方"),
			Remark:              col(rec, columns, "商品"),
			ExternalID:          col(rec, columns, "交易单号"),
			CounterpartyName:    col(rec, columns, "交易对方"),
			CounterpartyAccount: col(rec, columns, "商户单号"),
			Currency:            "CNY",
		}
		rows = append(rows, row)
	}
	return rows, mapping
}

// quoteOrEmpty 空值显示为 /。
func quoteOrEmpty(s string) string {
	if s == "" {
		return "/"
	}
	return s
}
