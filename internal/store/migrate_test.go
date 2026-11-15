package store

import (
	"path/filepath"
	"testing"
)

// TestMigrateFreezeColumns §9.10 T10 ①②：3 列 + 2 部分索引 + schema_version=3 + 幂等。
func TestMigrateFreezeColumns(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(); err != nil {
		t.Fatalf("首次 migrate 失败: %v", err)
	}

	cols := []string{"freeze_confirmed", "freeze_confirmed_at", "import_batch_id"}
	for _, c := range cols {
		ok, cerr := s.hasColumn("transactions", c)
		if cerr != nil {
			t.Fatal(cerr)
		}
		if !ok {
			t.Errorf("transactions 缺少列 %s", c)
		}
	}
	var ver string
	if err = s.SQL.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver != "3" {
		t.Errorf("schema_version = %q，want 3", ver)
	}
	// 部分索引存在
	for _, idx := range []string{"ix_tx_unconfirmed", "ix_tx_import_batch"} {
		var n int
		if err = s.SQL.QueryRow(`SELECT COUNT(1) FROM sqlite_master
			WHERE type='index' AND name = ?`, idx).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("索引 %s 不存在", idx)
		}
	}

	// 默认值：不写该列的 INSERT → 1（历史行/手工行保持「已冻结」语义）
	if _, err = s.SQL.Exec(`INSERT INTO accounts (id,name,account_type,value_type,currency,
		initial_balance_cents,is_count_in_total,sort_order,is_archived,created_at,updated_at)
		VALUES ('acc_def1','默认账户','asset','wallet','CNY',0,1,1,0,1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SQL.Exec(`INSERT INTO transactions
		(id, created_seq, transaction_time, local_date, transaction_type, amount_cents,
		 account_id, created_at, updated_at)
		VALUES ('2020010100000000001', 1, 100, '1970-01-01', 'expense', 100, 'acc_def1', 1, 1)`); err != nil {
		t.Fatalf("插入历史行失败: %v", err)
	}
	var fc int
	var batch string
	if err = s.SQL.QueryRow(`SELECT freeze_confirmed, import_batch_id FROM transactions
		WHERE id='2020010100000000001'`).Scan(&fc, &batch); err != nil {
		t.Fatal(err)
	}
	if fc != 1 {
		t.Errorf("历史行 freeze_confirmed = %d，want 1（默认值保证语义不变）", fc)
	}
	if batch != "" {
		t.Errorf("import_batch_id 默认应为空串，实得 %q", batch)
	}

	// 模拟老库（v1.2 无这三列）：删索引 + 删列 → 再 migrate 应补回且不报错
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS ix_tx_unconfirmed`,
		`DROP INDEX IF EXISTS ix_tx_import_batch`,
		`ALTER TABLE transactions DROP COLUMN freeze_confirmed`,
		`ALTER TABLE transactions DROP COLUMN freeze_confirmed_at`,
		`ALTER TABLE transactions DROP COLUMN import_batch_id`,
	} {
		if _, err = s.SQL.Exec(stmt); err != nil {
			t.Skipf("当前 SQLite 不支持 DROP COLUMN，跳过老库补列用例: %v", err)
		}
	}
	if ok, _ := s.hasColumn("transactions", "freeze_confirmed"); ok {
		t.Fatal("前置失败：列未删除")
	}
	if err = s.Migrate(); err != nil {
		t.Fatalf("老库补列 migrate 失败: %v", err)
	}
	for _, c := range cols {
		ok, cerr := s.hasColumn("transactions", c)
		if cerr != nil {
			t.Fatal(cerr)
		}
		if !ok {
			t.Errorf("补列后仍缺少 %s", c)
		}
	}
	// 已有行在补列后应拿到 1
	if err = s.SQL.QueryRow(`SELECT freeze_confirmed FROM transactions
		WHERE id='2020010100000000001'`).Scan(&fc); err != nil {
		t.Fatal(err)
	}
	if fc != 1 {
		t.Errorf("补列后历史行 freeze_confirmed = %d，want 1", fc)
	}
	// 视图必须能看到新列（t.* 展开）
	var viewCols int
	rows, err := s.SQL.Query(`PRAGMA table_info(v_transactions_signed)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, ctype      string
			dflt             any
		)
		if err = rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if name == "freeze_confirmed" {
			viewCols++
		}
	}
	rows.Close()
	if viewCols != 1 {
		t.Error("视图 v_transactions_signed 未展开 freeze_confirmed")
	}

	// 幂等：连续两次 migrate 不报错、版本不变
	for i := 0; i < 2; i++ {
		if err = s.Migrate(); err != nil {
			t.Fatalf("第 %d 次重复 migrate 失败: %v", i+1, err)
		}
	}
	if err = s.SQL.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver != "3" {
		t.Errorf("重复 migrate 后 schema_version = %q，want 3", ver)
	}
	// 数据未被破坏
	var rowCount int
	if err = s.SQL.QueryRow(`SELECT COUNT(1) FROM transactions`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Errorf("重复 migrate 后行数 = %d，want 1", rowCount)
	}
}
