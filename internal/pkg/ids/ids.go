// Package ids 实现 19 位业务 ID（yyyyMMddHHmmss + 4 位月度序列 + 校验码）
// 与短 ID（前缀 + 8 位小写 base32），以及月度/全局序列的原子分配。
// 算法见 docs/ARCHITECTURE.md §6.1。
package ids

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ledger/internal/pkg/apierr"
)

// Tx 抽象 *sql.Tx（便于测试与复用）。
type Tx interface {
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// CheckDigit 对 18 位数字主体计算校验码：
// 逐位求和 s，r = s % 11；r == 10 → 'X'，否则 '0'+r。
func CheckDigit(body18 string) byte {
	sum := 0
	for i := 0; i < 18; i++ {
		sum += int(body18[i] - '0')
	}
	if r := sum % 11; r == 10 {
		return 'X'
	} else {
		return byte('0' + r)
	}
}

// Verify 校验 19 位 ID（长度、前 18 位数字、校验码含 X 分支）。
func Verify(id string) bool {
	if len(id) != 19 {
		return false
	}
	for i := 0; i < 18; i++ {
		if id[i] < '0' || id[i] > '9' {
			return false
		}
	}
	c := id[18]
	okNum := c >= '0' && c <= '9'
	if !okNum && c != 'X' {
		return false
	}
	return CheckDigit(id[:18]) == c
}

var shortIDEncoding = base32.HexEncoding.WithPadding(base32.NoPadding)

// ShortID 生成 '<prefix>' + 8 位小写 base32hex（crypto/rand 5 字节）。
func ShortID(prefix string) string {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 不可用时退化为时间戳扰动（理论上不会发生）
		n := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(n >> (8 * i))
		}
	}
	return prefix + strings.ToLower(shortIDEncoding.EncodeToString(b[:]))
}

// AllocMonthSeq 返回当月可用序列号：首次为 0，之后 1,2,3...
// meta['seq:YYYYMM'] 语义 = "下一个待分配值"。必须在写事务内调用。
func AllocMonthSeq(tx Tx, ym string, nowUnix int64) (int, error) {
	key := "seq:" + ym
	var cur int
	err := tx.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = ?`, key).Scan(&cur)
	if err == sql.ErrNoRows {
		if _, err = tx.Exec(`INSERT INTO meta(key,value,updated_at) VALUES(?,?,?)`, key, "1", nowUnix); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(`UPDATE meta SET value=?, updated_at=? WHERE key=?`,
		strconv.Itoa(cur+1), nowUnix, key); err != nil {
		return 0, err
	}
	return cur, nil
}

// AllocGlobalSeq 分配全局单调递增 created_seq（永不复用）。必须在写事务内调用。
func AllocGlobalSeq(tx Tx, nowUnix int64) (int64, error) {
	var cur int64
	err := tx.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key='seq:global'`).Scan(&cur)
	if err == sql.ErrNoRows {
		if _, err = tx.Exec(`INSERT INTO meta(key,value,updated_at) VALUES('seq:global','2',?)`, nowUnix); err != nil {
			return 0, err
		}
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(`UPDATE meta SET value=?, updated_at=? WHERE key='seq:global'`, cur+1, nowUnix); err != nil {
		return 0, err
	}
	return cur, nil
}

// NewTransactionID 生成 19 位交易 ID，冲突时重试（同秒 + 序列回绕场景）。
// loc 为应用时区；t 会被转换到该时区取时间戳部分。
func NewTransactionID(tx Tx, t time.Time, loc *time.Location) (string, error) {
	ts := t.In(loc).Format("20060102150405")
	nowUnix := time.Now().Unix()
	for attempt := 0; attempt < 8; attempt++ {
		seq, err := AllocMonthSeq(tx, ts[:6], nowUnix)
		if err != nil {
			return "", err
		}
		body := ts + fmt.Sprintf("%04d", seq%10000) // seq 溢出 4 位时回绕
		id := body + string(CheckDigit(body))
		var n int
		if err = tx.QueryRow(`SELECT COUNT(1) FROM transactions WHERE id=?`, id).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return id, nil
		}
	}
	return "", apierr.New(apierr.CodeSequenceOverflow, "当月交易序列已耗尽，请检查数据")
}
