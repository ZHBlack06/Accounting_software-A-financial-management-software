package ids

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/glebarez/sqlite" // 注册 "sqlite" 驱动
)

// TestCheckDigitStable 校验码对随机 18 位主体稳定（同一输入恒同输出）。
func TestCheckDigitStable(t *testing.T) {
	bodies := []string{
		"202610031230150000", "199901010000000001", "202602280959599999",
		"000000000000000000", "999999999999999999",
	}
	for _, b := range bodies {
		first := CheckDigit(b)
		for i := 0; i < 5; i++ {
			if got := CheckDigit(b); got != first {
				t.Fatalf("CheckDigit(%q) 不稳定: %c vs %c", b, first, got)
			}
		}
	}
	// 手工验证：sum("202610031230150000") = 2+0+2+6+1+0+0+3+1+2+3+0+1+5+0+0+0+0 = 26 → 26%11=4 → '4'
	if got := CheckDigit("202610031230150000"); got != '4' {
		t.Fatalf("CheckDigit 计算错误: got %c, want '4'", got)
	}
}

// TestVerify X 分支与常规分支。
func TestVerify(t *testing.T) {
	// 构造一个校验码为 X 的 ID：需要 sum%11==10
	// sum("202610031230150001") = 27 → 27%11=5，不是 X；找一个 X 的例子：
	// "000000000000000001" sum=1 → '1'；逐步构造：sum=10 → X
	body := "000000000000000019" // sum = 1+9 = 10 → X
	id := body + "X"
	if !Verify(id) {
		t.Fatalf("Verify(%q) 应为 true（X 分支）", id)
	}
	// 文档示例：202610031230150000 → 校验码 '4'
	if !Verify("2026100312301500004") {
		t.Fatal("Verify(2026100312301500004) 应为 true")
	}
	if Verify("202610031230150000X") {
		t.Fatal("Verify(202610031230150000X) 应为 false（校验码不符）")
	}
	if Verify("2026100312301500000") {
		t.Fatal("Verify 错误校验码应为 false")
	}
	if Verify("20261003123015000") {
		t.Fatal("长度不足 19 应为 false")
	}
	if Verify("20261003123015000a4") {
		t.Fatal("含非数字应为 false")
	}
	if Verify("202610031230150000x") {
		t.Fatal("小写 x 应为 false")
	}
}

// TestNewTransactionID 连续 3 次调用：ID 长度 19、校验通过、created_seq 严格递增。
func TestNewTransactionID(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	var prevSeq int64
	for i := 0; i < 3; i++ {
		id, err := NewTransactionID(tx, time.Now(), time.Local)
		if err != nil {
			t.Fatalf("第 %d 次 NewTransactionID 失败: %v", i, err)
		}
		if len(id) != 19 {
			t.Fatalf("ID 长度 = %d, want 19 (%q)", len(id), id)
		}
		if !Verify(id) {
			t.Fatalf("ID 校验失败: %q", id)
		}
		// 把生成的 ID 写入 transactions，模拟真实占用
		seq, err := AllocGlobalSeq(tx, time.Now().Unix())
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && seq <= prevSeq {
			t.Fatalf("created_seq 未严格递增: %d <= %d", seq, prevSeq)
		}
		prevSeq = seq
		if _, err := tx.Exec(`INSERT INTO transactions(id, created_seq) VALUES(?,?)`, id, seq); err != nil {
			t.Fatal(err)
		}
	}
}

// TestAllocMonthSeq 同事务内连续 3 次 → 0,1,2；跨月 key 独立。
func TestAllocMonthSeq(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for i, want := range []int{0, 1, 2} {
		got, err := AllocMonthSeq(tx, "202610", now)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("AllocMonthSeq 第 %d 次 = %d, want %d", i, got, want)
		}
	}
	// 跨月独立
	got, err := AllocMonthSeq(tx, "202611", now)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("跨月序列应独立从 0 开始, got %d", got)
	}
}

// TestAllocGlobalSeq 连续 → 1,2,3。
func TestAllocGlobalSeq(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for i, want := range []int64{1, 2, 3} {
		got, err := AllocGlobalSeq(tx, now)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("AllocGlobalSeq 第 %d 次 = %d, want %d", i, got, want)
		}
	}
}

// openTestDB 内存库：仅建 meta + 极简 transactions（避免依赖 store 包造成环）。
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE transactions (id TEXT PRIMARY KEY CHECK (length(id)=19), created_seq INTEGER NOT NULL UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	return db
}
