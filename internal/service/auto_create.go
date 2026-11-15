package service

import (
	"database/sql"
	"fmt"
	"strings"

	"ledger/internal/pkg/ids"
	"ledger/internal/pkg/sheetparse"
	"ledger/internal/pkg/timeutil"
)

// palette 自动创建分类/标签时轮转使用的颜色（与种子分类调色板一致）。
var palette = []string{
	"#F2994A", "#2D9CDB", "#9B51E0", "#EB5757", "#F2C94C",
	"#27AE60", "#56CCF2", "#BB6BD9", "#6FCF97", "#828282",
}

// rowQuerier 兼容 *sql.DB / *sql.Tx 的单行查询接口。
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// CategoryNeed 一次导入需要「确保存在」的一个分类。
type CategoryNeed struct {
	Name            string `json:"name"`
	ParentName      string `json:"parent_name"`
	TransactionType string `json:"transaction_type"`
	Level           int    `json:"level"`
	Rows            int    `json:"rows"`
}

// CreatedRef 新建的分类/标签引用（响应里的 created_categories / created_tags）。
type CreatedRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CategoryResolution 分类解析结果。
type CategoryResolution struct {
	IDs      map[string]string // CategoryKey(parent,name) → id
	Created  []CreatedRef
	Warnings []string
}

// TagResolution 标签解析结果。
type TagResolution struct {
	IDs      map[string]string // name → id
	Created  []CreatedRef
	Warnings []string
}

// CategoryKey 分类唯一键（父名 + 名称，避免同名父子冲突）。
// 注意：名称只做 TrimSpace，不做任何归一化 —— 「其他」与「其它」是两个键（§4.5 铁律）。
func CategoryKey(parentName, name string) string {
	return sheetparse.NormalizeName(parentName) + "\x00" + sheetparse.NormalizeName(name)
}

// LookupCategory 查询分类（parentName 为空表示一级分类）。
// 返回 (id, transaction_type, found)；未命中 id 为空。
func LookupCategory(q rowQuerier, parentName, name string) (string, string, bool, error) {
	name = sheetparse.NormalizeName(name)
	if name == "" {
		return "", "", false, nil
	}
	var parentID string
	if p := sheetparse.NormalizeName(parentName); p != "" {
		id, _, found, err := LookupCategory(q, "", p)
		if err != nil {
			return "", "", false, err
		}
		if !found {
			return "", "", false, nil
		}
		parentID = id
	}
	var id, txType string
	err := q.QueryRow(`SELECT id, transaction_type FROM categories
		WHERE IFNULL(parent_id,'') = ? AND name = ?`, parentID, name).Scan(&id, &txType)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return id, txType, true, nil
}

// EnsureCategoriesTx 在写事务内确保分类存在（§4.5），返回 name → id 解析表。
//
// 名称处理只做 TrimSpace：不合并「其他 / 其它」，不做全半角/繁简/大小写归一。
// 批内使用 map 缓存；UNIQUE 冲突时回查一次再复用（吞掉并发竞争）。
func EnsureCategoriesTx(tx *sql.Tx, needs []CategoryNeed) (*CategoryResolution, error) {
	res := &CategoryResolution{IDs: map[string]string{}, Created: []CreatedRef{}, Warnings: []string{}}
	for _, need := range needs {
		name := sheetparse.NormalizeName(need.Name)
		if name == "" {
			continue
		}
		parentName := sheetparse.NormalizeName(need.ParentName)
		// 先确保父分类存在
		if parentName != "" {
			parentNeed := CategoryNeed{Name: parentName, TransactionType: need.TransactionType, Level: 1}
			if _, err := ensureOneCategoryTx(tx, res, parentNeed); err != nil {
				return nil, err
			}
		}
		if _, err := ensureOneCategoryTx(tx, res, need); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// ensureOneCategoryTx 确保单个分类存在，返回其 id。
func ensureOneCategoryTx(tx *sql.Tx, res *CategoryResolution, need CategoryNeed) (string, error) {
	name := sheetparse.NormalizeName(need.Name)
	parentName := sheetparse.NormalizeName(need.ParentName)
	key := CategoryKey(parentName, name)
	if id, ok := res.IDs[key]; ok {
		return id, nil
	}
	id, txType, found, err := LookupCategory(tx, parentName, name)
	if err != nil {
		return "", err
	}
	if found {
		res.IDs[key] = id
		if txType != "both" && need.TransactionType != "" && txType != need.TransactionType {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("分类「%s」已存在但类型为 %s（本行方向 %s），已复用且不修改原类型", name, txType, need.TransactionType))
		}
		return id, nil
	}

	var parentID *string
	level := 1
	if parentName != "" {
		pid, err2 := ensureOneCategoryTx(tx, res, CategoryNeed{
			Name: parentName, TransactionType: need.TransactionType, Level: 1})
		if err2 != nil {
			return "", err2
		}
		parentID = &pid
		level = 2
	}
	var next int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM categories`).Scan(&next); err != nil {
		return "", err
	}
	txType = need.TransactionType
	if txType != "income" {
		txType = "expense"
	}
	newID := ids.ShortID("cat_")
	now := timeutil.NowUnix()
	_, err = tx.Exec(`INSERT INTO categories
		(id, name, parent_id, level, transaction_type, is_system, is_archived, color, icon,
		 sort_order, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		newID, name, parentID, level, txType, 0, 0, palette[next%len(palette)], "",
		1000+next, now, now)
	if err != nil {
		// 唯一索引冲突（并发或重复）→ 回查一次复用
		if existing, _, found2, lerr := LookupCategory(tx, parentName, name); lerr == nil && found2 {
			res.IDs[key] = existing
			return existing, nil
		}
		return "", fmt.Errorf("创建分类「%s」失败: %w", name, err)
	}
	res.IDs[key] = newID
	res.Created = append(res.Created, CreatedRef{ID: newID, Name: name})
	return newID, nil
}

// EnsureTagsTx 在写事务内确保标签存在（tags.name UNIQUE，精确匹配复用）。
func EnsureTagsTx(tx *sql.Tx, names []string) (*TagResolution, error) {
	res := &TagResolution{IDs: map[string]string{}, Created: []CreatedRef{}, Warnings: []string{}}
	var next int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM tags`).Scan(&next); err != nil {
		return nil, err
	}
	for _, raw := range names {
		name := sheetparse.NormalizeName(raw)
		if name == "" {
			continue
		}
		if _, ok := res.IDs[name]; ok {
			continue
		}
		var id string
		err := tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&id)
		if err == nil {
			res.IDs[name] = id
			continue
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
		newID := ids.ShortID("tag_")
		now := timeutil.NowUnix()
		_, err = tx.Exec(`INSERT INTO tags
			(id, name, color, sort_order, use_count, is_archived, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?)`,
			newID, name, palette[next%len(palette)], (next+1)*10, 0, 0, now, now)
		if err != nil {
			if qerr := tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&id); qerr == nil {
				res.IDs[name] = id
				continue
			}
			return nil, fmt.Errorf("创建标签「%s」失败: %w", name, err)
		}
		next++
		res.IDs[name] = newID
		res.Created = append(res.Created, CreatedRef{ID: newID, Name: name})
	}
	return res, nil
}

// TagExists 查询标签是否已存在（preview 只统计不写库）。
func TagExists(q rowQuerier, name string) (bool, error) {
	name = sheetparse.NormalizeName(name)
	if name == "" {
		return false, nil
	}
	var id string
	err := q.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// splitTagsSafe 标签名去重（保持顺序）。
func splitTagsSafe(names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}
