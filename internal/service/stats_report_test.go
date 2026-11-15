package service_test

import (
	"context"
	"math"
	"testing"
	"time"

	"ledger/internal/pkg/timeutil"
	"ledger/internal/service"
	"ledger/internal/store"
)

// statsOf 用测试环境构造统计服务。
func (e *testEnv) statsOf() *service.StatsService {
	return service.NewStatsService(e.s, e.txr, e.mast, e.bal, store.NewMetaRepo(e.s))
}

// reportsOf 用测试环境构造报表服务。
func (e *testEnv) reportsOf() *service.ReportService {
	return service.NewReportService(e.s, e.txr, e.mast, e.bal, e.statsOf())
}

// dayRange 返回包含 offsetDays 那一天的 [00:00:00, 23:59:59]。
func dayRange(offsetDays int) (int64, int64) {
	loc := timeutil.Local()
	n := time.Now().In(loc).AddDate(0, 0, offsetDays)
	s := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc).Unix()
	return s, s + 86399
}

// spanRange 返回 [now-days 天 00:00, now 23:59:59]。
func spanRange(days int) (int64, int64) {
	loc := timeutil.Local()
	now := time.Now().In(loc)
	s := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -days).Unix()
	e := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, loc).Unix()
	return s, e
}

// TestStatsSummary T06 验收：收支/净额/笔数/账户总余额。
func TestStatsSummary(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001",
		CategoryID: catRef("cat_e01"), MerchantText: "星巴克",
	})
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "income", AmountCents: 5000, AccountID: "acc_ali0001",
		CategoryID: catRef("cat_i01"), MerchantText: "工资",
	})

	start, end := dayRange(0)
	q := &store.TxQuery{StartTime: &start, EndTime: &end}
	st := e.statsOf()

	sum, err := st.Summary(context.Background(), q, false)
	if err != nil {
		t.Fatalf("Summary 失败: %v", err)
	}
	if sum.IncomeCents != 5000 || sum.ExpenseCents != 3000 {
		t.Fatalf("income/expense = %d/%d, want 5000/3000", sum.IncomeCents, sum.ExpenseCents)
	}
	if sum.NetCents != 2000 {
		t.Fatalf("net = %d, want 2000", sum.NetCents)
	}
	if sum.TransactionCount != 2 {
		t.Fatalf("count = %d, want 2", sum.TransactionCount)
	}
	if sum.TotalBalanceCents != 152000 {
		t.Fatalf("total_balance = %d, want 152000", sum.TotalBalanceCents)
	}
	if sum.DayCount != 1 || sum.AvgExpensePerDayCents != 3000 {
		t.Fatalf("day_count/avg = %d/%d, want 1/3000", sum.DayCount, sum.AvgExpensePerDayCents)
	}
	if sum.AdjustmentIncomeCents != 0 || sum.AdjustmentExpenseCents != 0 {
		t.Fatalf("无调整时调整额应为 0: %+v", sum)
	}
}

// TestStatsSummaryAdjustSplit T06 验收：调整分录与普通收支分开统计。
func TestStatsSummaryAdjustSplit(t *testing.T) {
	e := newTestEnv(t)
	id := seedFrozenExpense(t, e, 10000) // 40 天前支出 100 元（冻结区）
	if _, err := e.adjust.Adjust(context.Background(), id, &service.AdjustInput{
		Mode: "adjust", NewAmountCents: 1000, Remark: "多记 90 元冲减",
	}); err != nil {
		t.Fatalf("adjust 失败: %v", err)
	}

	start, end := spanRange(100)
	q := &store.TxQuery{StartTime: &start, EndTime: &end}
	st := e.statsOf()

	withAdj, err := st.Summary(context.Background(), q, false)
	if err != nil {
		t.Fatal(err)
	}
	// 普通支出 10000 + 普通收入 0 + 调整收入 9000
	if withAdj.ExpenseCents != 10000 || withAdj.IncomeCents != 9000 {
		t.Fatalf("含调整 income/expense = %d/%d, want 9000/10000",
			withAdj.IncomeCents, withAdj.ExpenseCents)
	}
	if withAdj.AdjustmentIncomeCents != 9000 || withAdj.AdjustmentExpenseCents != 0 {
		t.Fatalf("调整额 = %d/%d, want 9000/0",
			withAdj.AdjustmentIncomeCents, withAdj.AdjustmentExpenseCents)
	}
	if withAdj.NetCents != -1000 {
		t.Fatalf("net = %d, want -1000", withAdj.NetCents)
	}

	withoutAdj, err := st.Summary(context.Background(), q, true)
	if err != nil {
		t.Fatal(err)
	}
	if withoutAdj.ExpenseCents != 10000 || withoutAdj.IncomeCents != 0 {
		t.Fatalf("排除调整 income/expense = %d/%d, want 0/10000",
			withoutAdj.IncomeCents, withoutAdj.ExpenseCents)
	}
	if !withoutAdj.ExcludeAdjustments {
		t.Fatal("exclude_adjustments 应为 true")
	}
	// 调整只改余额口径，总余额必须体现 -1000 的净影响
	if withAdj.TotalBalanceCents != withoutAdj.TotalBalanceCents {
		t.Fatalf("total_balance 不应随 exclude_adjustments 变化: %d vs %d",
			withAdj.TotalBalanceCents, withoutAdj.TotalBalanceCents)
	}
	if withAdj.TotalBalanceCents != 149000 {
		t.Fatalf("total_balance = %d, want 149000", withAdj.TotalBalanceCents)
	}
}

// TestStatsTrend day 分桶 + 超 92 天自动降级 week。
func TestStatsTrend(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e01"),
	})
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "income", AmountCents: 5000, AccountID: "acc_ali0001", CategoryID: catRef("cat_i01"),
	})
	st := e.statsOf()

	// 单日区间：1 个桶
	start, end := dayRange(0)
	q := &store.TxQuery{StartTime: &start, EndTime: &end}
	tr, err := st.Trend(context.Background(), q, "day")
	if err != nil {
		t.Fatalf("Trend 失败: %v", err)
	}
	if tr.Granularity != "day" || tr.Downgraded {
		t.Fatalf("granularity/downgraded = %s/%v, want day/false", tr.Granularity, tr.Downgraded)
	}
	if len(tr.Points) != 1 {
		t.Fatalf("points = %d, want 1", len(tr.Points))
	}
	p := tr.Points[0]
	if p.IncomeCents != 5000 || p.ExpenseCents != 3000 || p.NetCents != 2000 || p.Count != 2 {
		t.Fatalf("桶数据错误: %+v", p)
	}

	// 100 天区间：day 自动降级 week，且空桶补 0
	s2, e2 := spanRange(100)
	q2 := &store.TxQuery{StartTime: &s2, EndTime: &e2}
	tr2, err := st.Trend(context.Background(), q2, "day")
	if err != nil {
		t.Fatal(err)
	}
	if tr2.Granularity != "week" || !tr2.Downgraded {
		t.Fatalf("超 92 天应降级 week: %s/%v", tr2.Granularity, tr2.Downgraded)
	}
	if len(tr2.Points) == 0 {
		t.Fatal("降级后应有周桶")
	}
	var totalExp int64
	for _, pt := range tr2.Points {
		totalExp += pt.ExpenseCents
	}
	if totalExp != 3000 {
		t.Fatalf("周桶支出合计 = %d, want 3000（空桶补 0 不应丢数据）", totalExp)
	}

	// 缺区间 → 报错
	if _, err = st.Trend(context.Background(), &store.TxQuery{}, "day"); err == nil {
		t.Fatal("缺 start/end 应报错")
	}
}

// TestStatsCategoryBreakdown 分类占比（level=1 汇总、level=2 带 children）。
func TestStatsCategoryBreakdown(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e01"),
	})
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 2000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e02"),
	})
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "income", AmountCents: 9000, AccountID: "acc_ali0001", CategoryID: catRef("cat_i01"),
	})
	start, end := dayRange(0)
	q := &store.TxQuery{StartTime: &start, EndTime: &end}
	st := e.statsOf()

	bd, err := st.CategoryBreakdown(context.Background(), q, "expense", 1)
	if err != nil {
		t.Fatalf("CategoryBreakdown 失败: %v", err)
	}
	if bd.TotalCents != 5000 {
		t.Fatalf("total = %d, want 5000（收入不计入支出占比）", bd.TotalCents)
	}
	byName := map[string]int64{}
	for _, it := range bd.Items {
		byName[it.Name] = it.AmountCents
	}
	if byName["餐饮"] != 3000 || byName["交通"] != 2000 {
		t.Fatalf("分类金额错误: %+v", byName)
	}
	for _, it := range bd.Items {
		if math.Abs(it.Percent-float64(it.AmountCents)*100/5000) > 0.01 {
			t.Fatalf("%s 占比 = %v, want %v", it.Name, it.Percent, float64(it.AmountCents)*100/5000)
		}
	}
}

// TestReportCategory 分类报表：环比上期金额与百分比。
func TestReportCategory(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e01"),
	})
	// prev_period 是等长上移：今日区间的上期 = 昨日
	prev := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 1500, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e01"),
	})
	yesterday := time.Now().AddDate(0, 0, -1).Unix()
	if err := e.txr.SetTransactionTimeForTest(prev.Primary.ID, yesterday); err != nil {
		t.Fatal(err)
	}

	start, end := dayRange(0)
	q := &store.TxQuery{StartTime: &start, EndTime: &end}
	rp := e.reportsOf()
	res, err := rp.CategoryReport(context.Background(), q, "expense", 1, "prev_period", false)
	if err != nil {
		t.Fatalf("CategoryReport 失败: %v", err)
	}
	if res.TotalCents != 3000 {
		t.Fatalf("total = %d, want 3000", res.TotalCents)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(res.Items))
	}
	it := res.Items[0]
	if it.AmountCents != 3000 || it.TransactionCount != 1 {
		t.Fatalf("本期金额/笔数错误: %+v", it)
	}
	if math.Abs(it.Percent-100) > 0.01 {
		t.Fatalf("单一分类占比 = %v, want 100", it.Percent)
	}
	if it.PrevPeriodCents != 1500 {
		t.Fatalf("上期金额 = %d, want 1500", it.PrevPeriodCents)
	}
	if it.PrevPeriodPct == nil || math.Abs(*it.PrevPeriodPct-100) > 0.01 {
		t.Fatalf("环比 = %v, want 100%%", it.PrevPeriodPct)
	}

	// 必填区间
	if _, err = rp.CategoryReport(context.Background(), &store.TxQuery{}, "expense", 1, "none", false); err == nil {
		t.Fatal("缺 start/end 应报错")
	}
}

// TestReportAccount 账户报表：收支/转账/余额口径。
func TestReportAccount(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e01"),
	})
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "income", AmountCents: 5000, AccountID: "acc_ali0001", CategoryID: catRef("cat_i01"),
	})
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "transfer", AmountCents: 20000,
		Transfer: &service.TransferInput{FromAccountID: "acc_cmb0001", ToAccountID: "acc_ali0001"},
	})
	start, end := dayRange(0)
	q := &store.TxQuery{StartTime: &start, EndTime: &end}

	res, err := e.reportsOf().AccountReport(context.Background(), q)
	if err != nil {
		t.Fatalf("AccountReport 失败: %v", err)
	}
	byAcc := map[string]service.AccountReportItem{}
	for _, it := range res.Items {
		byAcc[it.AccountID] = it
	}
	acc1, ok := byAcc["acc_cmb0001"]
	if !ok {
		t.Fatalf("缺少账户 acc_cmb0001: %+v", res.Items)
	}
	if acc1.ExpenseCents != 3000 || acc1.TransferOutCents != 20000 || acc1.IncomeCents != 0 {
		t.Fatalf("acc1 汇总错误: %+v", acc1)
	}
	if acc1.BalanceCents != 77000 {
		t.Fatalf("acc1 余额 = %d, want 77000", acc1.BalanceCents)
	}
	acc2 := byAcc["acc_ali0001"]
	if acc2.IncomeCents != 5000 || acc2.TransferInCents != 20000 {
		t.Fatalf("acc2 汇总错误: %+v", acc2)
	}
	if acc2.BalanceCents != 75000 {
		t.Fatalf("acc2 余额 = %d, want 75000", acc2.BalanceCents)
	}
	if res.TotalBalanceCents != 152000 {
		t.Fatalf("total_balance = %d, want 152000（转账双边抵消）", res.TotalBalanceCents)
	}
}

// TestReportPeriodCompare 月度周期对比：本期 vs 上期。
func TestReportPeriodCompare(t *testing.T) {
	e := newTestEnv(t)
	e.setInitial(t)
	e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 3000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e01"),
	})
	old := e.mustCreate(t, &service.CreateTxnInput{
		TransactionType: "expense", AmountCents: 2000, AccountID: "acc_cmb0001", CategoryID: catRef("cat_e02"),
	})
	past := time.Now().AddDate(0, 0, -40).Unix()
	if err := e.txr.SetTransactionTimeForTest(old.Primary.ID, past); err != nil {
		t.Fatal(err)
	}

	base := time.Now().In(timeutil.Local()).Format("2006-01")
	res, err := e.reportsOf().PeriodCompare(context.Background(), "month", base, false)
	if err != nil {
		t.Fatalf("PeriodCompare 失败: %v", err)
	}
	if res.Period != "month" || res.Current.Label != base {
		t.Fatalf("period/label 错误: %s/%s", res.Period, res.Current.Label)
	}
	if res.Current.ExpenseCents != 3000 {
		t.Fatalf("本期支出 = %d, want 3000", res.Current.ExpenseCents)
	}
	// 40 天前落在上一个月（跨月时落在上期或上上期，至少不应计入本期）
	if res.Previous.ExpenseCents == 3000 {
		t.Fatalf("上期不应含本期数据: %+v", res.Previous)
	}
	if res.Current.NetCents != res.Current.IncomeCents-res.Current.ExpenseCents {
		t.Fatal("net 应为 income-expense")
	}
}
