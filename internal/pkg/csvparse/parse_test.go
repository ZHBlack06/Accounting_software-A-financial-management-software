package csvparse

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"ledger/internal/pkg/apierr"
)

// wechatCSV 微信账单：UTF-8 BOM + 16 行说明 + 表头 + 4 行数据（含 "/" 中性行）。
const wechatCSV = "\ufeff微信支付账单明细\n" +
	"微信昵称：[测试]\n" +
	"起始时间：[2026-09-01 00:00:00] 终止时间：[2026-10-01 00:00:00]\n" +
	"导出类型：[全部]\n" +
	"导出时间：[2026-10-02 10:00:00]\n" +
	"共 4 笔记录\n" +
	"1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n" +
	"交易时间,交易类型,交易对方,商品,收/支,金额(元),支付方式,当前状态,交易单号,商户单号,备注\n" +
	"2026-09-05 12:30:00,商户消费,星巴克,咖啡,支出,¥33.00,零钱,支付成功,10001234567890,M001,/\n" +
	"2026-09-06 09:00:00,商户消费,公司,报销,收入,100.00,零钱,已存入零钱,10001234567891,M002,/\n" +
	"2026-09-07 10:00:00,零钱提现,招商银行,提现,/,1000.00,零钱,提现已到账,10001234567892,M003,/\n" +
	"2026-09-08 18:00:00,商户消费,某餐厅,晚餐,支出,\"¥1,234.56\",零钱,已全额退款,10001234567893,M004,/\n"

// TestParseWeChat 微信：BOM + 说明行跳过 + / 跳过 + 金额解析 + 退款 warning。
func TestParseWeChat(t *testing.T) {
	f, err := Parse([]byte(wechatCSV))
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if f.Source != "wechat" {
		t.Fatalf("source = %s, want wechat", f.Source)
	}
	if f.Encoding != "utf-8" {
		t.Fatalf("encoding = %s, want utf-8", f.Encoding)
	}
	if len(f.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(f.Rows))
	}
	// 第 1 行：支出 3300
	if f.Rows[0].Parsed == nil || f.Rows[0].Parsed.Type != "expense" ||
		f.Rows[0].Parsed.AmountCents != 3300 {
		t.Fatalf("第 1 行解析错误: %+v", f.Rows[0].Parsed)
	}
	if f.Rows[0].Parsed.MerchantText != "星巴克" || f.Rows[0].Parsed.ExternalID != "10001234567890" {
		t.Fatalf("商户/单号映射错误: %+v", f.Rows[0].Parsed)
	}
	// 第 2 行：收入 10000
	if f.Rows[1].Parsed == nil || f.Rows[1].Parsed.Type != "income" ||
		f.Rows[1].Parsed.AmountCents != 10000 {
		t.Fatalf("第 2 行解析错误: %+v", f.Rows[1].Parsed)
	}
	// 第 3 行：收/支为 / → 跳过
	if f.Rows[2].Parsed != nil || f.Rows[2].SkipReason == "" {
		t.Fatalf("中性交易行应跳过: %+v", f.Rows[2])
	}
	// 第 4 行：¥1,234.56 → 123456 且带退款 warning
	if f.Rows[3].Parsed == nil || f.Rows[3].Parsed.AmountCents != 123456 {
		t.Fatalf("金额解析错误: %+v", f.Rows[3].Parsed)
	}
	if len(f.Rows[3].Warnings) == 0 || !strings.Contains(f.Rows[3].Warnings[0], "退款") {
		t.Fatalf("退款行应有 warning: %+v", f.Rows[3].Warnings)
	}
	// 指纹：无单号时可生成（同输入稳定）
	fp1 := Fingerprint("2026-09-05", 3300, "acc_x", "星巴克", "expense")
	fp2 := Fingerprint("2026-09-05", 3300, "acc_x", "星巴克", "expense")
	if fp1 != fp2 || len(fp1) != 16 {
		t.Fatalf("行指纹错误: %s vs %s", fp1, fp2)
	}
}

// alipayCSV 支付宝账单（GBK 编码，双列金额）。
const alipayCSV = "支付宝交易记录明细查询\n" +
	"账号:[test@example.com]\n" +
	"起始日期:[2026-09-01 00:00:00] 终止日期:[2026-10-01 00:00:00]\n" +
	"交易时间,交易分类,交易对方,对方账号,商品说明,收/支,金额,收/付款方式,交易状态,交易订单号,商家订单号,备注\n" +
	"2026-09-10 08:15:00,餐饮,早餐店,shop@x.com,包子,支出,15.00,余额宝,交易成功,20260910001,B001,\n" +
	"2026-09-11 19:00:00,购物,淘宝,taobao@x.com,键盘,支出,459.00,花呗,交易关闭,20260911001,B002,\n" +
	"2026-09-12 10:00:00,转账,朋友,friend@x.com,还款,不计收支,200.00,余额,交易成功,20260912001,B003,\n"

// TestParseAlipayGBK 支付宝：GBK 转 UTF-8 + 状态过滤 + 不计收支跳过。
func TestParseAlipayGBK(t *testing.T) {
	gbk, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(alipayCSV))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(gbk)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if f.Source != "alipay" || f.Encoding != "gbk" {
		t.Fatalf("source/encoding = %s/%s", f.Source, f.Encoding)
	}
	if len(f.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(f.Rows))
	}
	if f.Rows[0].Parsed == nil || f.Rows[0].Parsed.AmountCents != 1500 ||
		f.Rows[0].Parsed.Type != "expense" {
		t.Fatalf("第 1 行解析错误: %+v", f.Rows[0].Parsed)
	}
	if f.Rows[1].Parsed != nil || !strings.Contains(f.Rows[1].SkipReason, "交易关闭") {
		t.Fatalf("交易关闭行应跳过: %+v", f.Rows[1])
	}
	if f.Rows[2].Parsed != nil || !strings.Contains(f.Rows[2].SkipReason, "不计收支") {
		t.Fatalf("不计收支行应跳过: %+v", f.Rows[2])
	}
}

// alipayDualCSV 支付宝双列金额（收入金额/支出金额）。
const alipayDualCSV = "交易时间,交易对方,商品说明,收入金额,支出金额,交易状态,交易订单号\n" +
	"2026-09-15 09:00:00,公司,工资,5000.00,,交易成功,20260915001\n" +
	"2026-09-16 12:00:00,餐厅,午饭,,¥66.00,交易成功,20260916001\n"

// TestParseAlipayDualColumn 双列择一。
func TestParseAlipayDualColumn(t *testing.T) {
	f, err := Parse([]byte(alipayDualCSV))
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if f.Source != "alipay" {
		t.Fatalf("source = %s", f.Source)
	}
	if f.Rows[0].Parsed == nil || f.Rows[0].Parsed.Type != "income" || f.Rows[0].Parsed.AmountCents != 500000 {
		t.Fatalf("收入列解析错误: %+v", f.Rows[0].Parsed)
	}
	if f.Rows[1].Parsed == nil || f.Rows[1].Parsed.Type != "expense" || f.Rows[1].Parsed.AmountCents != 6600 {
		t.Fatalf("支出列解析错误: %+v", f.Rows[1].Parsed)
	}
}

// TestUnsupported 无法识别 → 422 UNSUPPORTED_CSV + detected_headers。
func TestUnsupported(t *testing.T) {
	bad := "姓名,年龄\n张三,18\n李四,20\n"
	_, err := Parse([]byte(bad))
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeUnsupportedCSV {
		t.Fatalf("应返回 UNSUPPORTED_CSV, got %v", err)
	}
	if ae.Details["detected_headers"] == nil {
		t.Fatal("details.detected_headers 缺失")
	}
}
