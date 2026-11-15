package csvparse

import "strings"

// parseAlipay 支付宝账单映射（§6.9 支付宝映射表）。
// 支持两种导出：单列「金额」+「收/支」，或「收入金额」/「支出金额」双列择一。
// 交易状态 交易关闭/等待付款 → 跳过；收/支 不计收支 → 跳过。
func parseAlipay(body [][]string, columns []string, headerIdx int) ([]Row, map[string]string) {
	mapping := map[string]string{
		"transaction_time": "交易时间",
		"transaction_type": "收/支",
		"amount":           "金额",
		"counterparty":     "交易对方",
		"remark":           "商品说明",
		"external_id":      "交易订单号",
		"status":           "交易状态",
	}
	var rows []Row
	for i, rec := range body {
		raw := rawMap(columns, rec)
		if len(raw) == 0 {
			continue
		}
		row := Row{RowNo: i + 2, Raw: raw, Warnings: []string{}}
		_ = headerIdx

		status := col(rec, columns, "交易状态")
		if strings.Contains(status, "交易关闭") || strings.Contains(status, "等待付款") {
			row.SkipReason = "交易状态为「" + status + "」"
			rows = append(rows, row)
			continue
		}
		dir := col(rec, columns, "收/支")
		if strings.Contains(dir, "不计收支") {
			row.SkipReason = "收/支为「不计收支」"
			rows = append(rows, row)
			continue
		}
		// 金额：双列择一 → 单列
		incomeStr := col(rec, columns, "收入金额")
		expenseStr := col(rec, columns, "支出金额")
		amountStr := col(rec, columns, "金额")
		var typ string
		var amount int64
		var err error
		switch {
		case incomeStr != "" && incomeStr != "--":
			typ = "income"
			amount, err = parseAmount(incomeStr)
			mapping["amount"] = "收入金额"
		case expenseStr != "" && expenseStr != "--":
			typ = "expense"
			amount, err = parseAmount(expenseStr)
			mapping["amount"] = "支出金额"
		default:
			switch dir {
			case "支出":
				typ = "expense"
			case "收入":
				typ = "income"
			}
			amount, err = parseAmount(amountStr)
		}
		if typ == "" {
			row.SkipReason = "无法判断收支方向（收/支=" + quoteOrEmpty(dir) + "）"
			rows = append(rows, row)
			continue
		}
		if err != nil || amount <= 0 {
			row.SkipReason = "金额解析失败: " + amountStr
			rows = append(rows, row)
			continue
		}
		timeStr := col(rec, columns, "交易时间", "发生时间")
		t, err := parseTime(timeStr)
		if err != nil {
			row.SkipReason = "交易时间格式非法: " + timeStr
			rows = append(rows, row)
			continue
		}
		external := col(rec, columns, "交易订单号", "业务流水号", "商家订单号")
		row.Parsed = &Parsed{
			TimeUnix:            t,
			TimeStr:             timeStr,
			Type:                typ,
			AmountCents:         amount,
			MerchantText:        col(rec, columns, "交易对方"),
			Remark:              col(rec, columns, "商品说明", "商品名称"),
			ExternalID:          external,
			CounterpartyName:    col(rec, columns, "交易对方"),
			CounterpartyAccount: col(rec, columns, "对方账号"),
			Currency:            "CNY",
		}
		rows = append(rows, row)
	}
	return rows, mapping
}
