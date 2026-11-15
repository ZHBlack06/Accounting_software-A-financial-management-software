package http_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestFreezeConfirmEndpoint 端点 75：三种入参、幂等、参数校验、DTO 状态翻转。
func TestFreezeConfirmEndpoint(t *testing.T) {
	a, srv, token := newTestAppFull(t)

	// 默认账户
	st, accs := doJSON(t, srv, token, "GET", "/api/accounts", nil)
	if st != 200 {
		t.Fatalf("accounts 状态 = %d", st)
	}
	items, _ := accs["items"].([]any)
	accountID := items[0].(map[string]any)["id"].(string)

	// 直接落 3 行「待确认」的历史交易（走 API 无法构造该状态，测试里用 SQL 注入）
	old := time.Now().AddDate(0, 0, -100).Unix()
	localDate := time.Unix(old, 0).Format("2006-01-02")
	var ids []string
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("20200101000000%04d1", i)
		if _, err := a.DB.SQL.Exec(`INSERT INTO transactions
			(id, created_seq, transaction_time, local_date, transaction_type, amount_cents,
			 account_id, source, external_id, freeze_confirmed, import_batch_id,
			 created_at, updated_at)
			VALUES (?,?,?,?, 'expense', ?, ?, 'import', ?, 0, 'imp_testbatch', ?, ?)`,
			id, 100+i, old+int64(i), localDate, 1000+i, accountID, "imp:"+id, old, old); err != nil {
			t.Fatalf("注入待确认行失败: %v", err)
		}
		ids = append(ids, id)
	}

	// 待确认行的 DTO：freeze_pending=true、is_frozen=false
	st, one := doJSON(t, srv, token, "GET", "/api/transactions/"+ids[0], nil)
	if st != 200 {
		t.Fatalf("取交易失败: %d %v", st, one)
	}
	oneTx, _ := one["transaction"].(map[string]any)
	if oneTx == nil {
		t.Fatalf("响应缺少 transaction: %v", one)
	}
	if oneTx["freeze_pending"] != true {
		t.Errorf("freeze_pending = %v，want true", one["freeze_pending"])
	}
	if oneTx["is_frozen"] != false {
		t.Errorf("待确认行 is_frozen = %v，want false（可写）", oneTx["is_frozen"])
	}
	if oneTx["import_batch_id"] != "imp_testbatch" {
		t.Errorf("import_batch_id = %v", oneTx["import_batch_id"])
	}

	// 参数校验：空入参 / all 未 confirm / 超 5000
	if st, body := doJSON(t, srv, token, "POST", "/api/transactions/freeze-confirm", map[string]any{}); st != 400 {
		t.Errorf("空入参状态 = %d，body=%v", st, body)
	}
	if st, _ := doJSON(t, srv, token, "POST", "/api/transactions/freeze-confirm",
		map[string]any{"all": true}); st != 400 {
		t.Errorf("all 未 confirm 状态 = %d，want 400", st)
	}
	many := make([]string, 5001)
	for i := range many {
		many[i] = fmt.Sprintf("%019d", i)
	}
	if st, _ := doJSON(t, srv, token, "POST", "/api/transactions/freeze-confirm",
		map[string]any{"transaction_ids": many}); st != 400 {
		t.Errorf("超过 5000 个 id 状态 = %d，want 400", st)
	}

	// 正常确认（混入一个不存在的 id → skipped_missing=1）
	st, res := doJSON(t, srv, token, "POST", "/api/transactions/freeze-confirm",
		map[string]any{"transaction_ids": append([]string{"2020010100000099999"}, ids...)})
	if st != 200 {
		t.Fatalf("确认状态 = %d，body=%v", st, res)
	}
	if res["confirmed"].(float64) != 3 {
		t.Errorf("confirmed = %v，want 3", res["confirmed"])
	}
	if res["skipped_missing"].(float64) != 1 {
		t.Errorf("skipped_missing = %v，want 1", res["skipped_missing"])
	}
	if res["materialized"] != true {
		t.Errorf("materialized = %v，want true", res["materialized"])
	}
	if res["pending_remaining"].(float64) != 0 {
		t.Errorf("pending_remaining = %v，want 0", res["pending_remaining"])
	}

	// 确认后：is_frozen=true、freeze_pending=false、freeze_confirmed_at 非空
	st, one2 := doJSON(t, srv, token, "GET", "/api/transactions/"+ids[0], nil)
	if st != 200 {
		t.Fatalf("取交易失败: %d", st)
	}
	one2Tx, _ := one2["transaction"].(map[string]any)
	if one2Tx == nil {
		t.Fatalf("响应缺少 transaction: %v", one2)
	}
	if one2Tx["is_frozen"] != true || one2Tx["freeze_pending"] != false {
		t.Errorf("确认后状态异常: is_frozen=%v freeze_pending=%v",
			one2Tx["is_frozen"], one2Tx["freeze_pending"])
	}
	if one2Tx["freeze_confirmed_at"] == nil {
		t.Error("确认后 freeze_confirmed_at 不应为 null")
	}

	// ⑦ 确认后**列表**接口：原待确认行 is_frozen=true、freeze_pending=false
	st, list := doJSON(t, srv, token, "GET", "/api/transactions?page=1&page_size=200", nil)
	if st != 200 {
		t.Fatalf("列表状态 = %d", st)
	}
	var found bool
	if items, ok := list["items"].([]any); ok {
		for _, it := range items {
			m, _ := it.(map[string]any)
			if m["id"] != ids[0] {
				continue
			}
			found = true
			if m["is_frozen"] != true || m["freeze_pending"] != false {
				t.Errorf("列表里确认后状态异常: is_frozen=%v freeze_pending=%v",
					m["is_frozen"], m["freeze_pending"])
			}
		}
	}
	if !found {
		t.Errorf("列表里应能找到刚确认的行 %s", ids[0])
	}

	// 幂等：再确认一次 → confirmed=0
	st, res2 := doJSON(t, srv, token, "POST", "/api/transactions/freeze-confirm",
		map[string]any{"transaction_ids": ids})
	if st != 200 {
		t.Fatalf("二次确认状态 = %d", st)
	}
	if res2["confirmed"].(float64) != 0 {
		t.Errorf("二次确认 confirmed = %v，want 0", res2["confirmed"])
	}

	// 确认后写保护恢复：PUT 金额 → 409 FROZEN
	st, body := doJSON(t, srv, token, "PUT", "/api/transactions/"+ids[0],
		map[string]any{"amount_cents": 999})
	if st != http.StatusConflict {
		t.Fatalf("确认后改金额状态 = %d，want 409，body=%v", st, body)
	}
	if e, _ := body["error"].(map[string]any); e["code"] != "FROZEN" {
		t.Errorf("错误码 = %v，want FROZEN", e["code"])
	}

	// batch_id 形态：另造 1 行同批次
	id4 := "2020010100000099001"
	if _, err := a.DB.SQL.Exec(`INSERT INTO transactions
		(id, created_seq, transaction_time, local_date, transaction_type, amount_cents,
		 account_id, source, external_id, freeze_confirmed, import_batch_id, created_at, updated_at)
		VALUES (?,?,?,?, 'expense', 5000, ?, 'import', ?, 0, 'imp_testbatch', ?, ?)`,
		id4, 200, old, localDate, accountID, "imp:"+id4, old, old); err != nil {
		t.Fatal(err)
	}
	st, res3 := doJSON(t, srv, token, "POST", "/api/transactions/freeze-confirm",
		map[string]any{"batch_id": "imp_testbatch"})
	if st != 200 || res3["confirmed"].(float64) != 1 {
		t.Fatalf("按批次确认结果 = %d %v", st, res3)
	}
	// verify：ok + pending_rows=0 + gaps=0
	st, vr := doJSON(t, srv, token, "GET", "/api/maintenance/verify", nil)
	if st != 200 {
		t.Fatalf("verify 状态 = %d", st)
	}
	if vr["ok"] != true || vr["pending_rows"].(float64) != 0 {
		t.Errorf("verify 异常: ok=%v pending=%v gaps=%v", vr["ok"], vr["pending_rows"], vr["materialization_gaps"])
	}
	if gaps, _ := vr["materialization_gaps"].([]any); len(gaps) != 0 {
		t.Errorf("gaps 应为空，实得 %v", gaps)
	}
}
