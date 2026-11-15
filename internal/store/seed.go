package store

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	"ledger/internal/pkg/timeutil"
)

//go:embed regions_seed.json
var regionsSeedJSON []byte

// Seed 首次启动（accounts 为空）时写入种子数据；地区字典每次启动幂等补齐。
// 种子口径见 docs/ARCHITECTURE.md §10.6。
func (s *Store) Seed() error {
	if err := s.seedRegions(); err != nil {
		return err
	}
	var n int64
	if err := s.G.Model(&Account{}).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil // 非全新库，跳过业务种子
	}
	now := timeutil.NowUnix()
	meta := NewMetaRepo(s)

	// ---- auth_token（64 hex）----
	tok, err := meta.Get("auth_token")
	if err != nil {
		return err
	}
	if tok == "" {
		b := make([]byte, 32)
		if _, err = rand.Read(b); err != nil {
			return fmt.Errorf("生成 auth_token 失败: %w", err)
		}
		newToken := hex.EncodeToString(b)
		if err = meta.Set("auth_token", newToken); err != nil {
			return err
		}
		// 仅首次生成时打印（已有令牌的库不再输出，避免密钥反复写入日志）
		slog.Info("首次初始化：已生成访问令牌，请复制保存（也可用 ./ledger.exe --print-token 再次查看）",
			"token", newToken)
	}

	// ---- 分类 ----
	type catSeed struct {
		id    string
		name  string
		color string
		subs  []string
	}
	expenseCats := []catSeed{
		{"cat_e01", "餐饮", "#F2994A", []string{"早餐", "午餐", "晚餐"}},
		{"cat_e02", "交通", "#2D9CDB", []string{"地铁", "公交", "打车", "加油"}},
		{"cat_e03", "居住", "#9B51E0", []string{"房租", "水电", "物业"}},
		{"cat_e04", "购物", "#EB5757", []string{"日用", "服饰", "数码"}},
		{"cat_e05", "娱乐", "#F2C94C", []string{"电影", "游戏", "旅行"}},
		{"cat_e06", "医疗健康", "#27AE60", []string{"门诊", "药品", "体检"}},
		{"cat_e07", "教育", "#56CCF2", []string{"书籍", "课程", "培训"}},
		{"cat_e08", "人情", "#BB6BD9", []string{"红包", "礼物"}},
		{"cat_e09", "金融", "#6FCF97", []string{"手续费", "利息"}},
		{"cat_e10", "其他", "#828282", []string{"其他", "杂项"}},
	}
	incomeCats := []catSeed{
		{"cat_i01", "工资", "#27AE60", nil},
		{"cat_i02", "奖金", "#6FCF97", nil},
		{"cat_i03", "兼职外快", "#56CCF2", nil},
		{"cat_i04", "投资理财", "#F2C94C", nil},
		{"cat_i05", "退款报销", "#9B51E0", nil},
		{"cat_i06", "红包", "#EB5757", nil},
		{"cat_i07", "其他收入", "#828282", nil},
	}
	for i, c := range expenseCats {
		parent := Category{ID: c.id, Name: c.name, Level: 1, TransactionType: "expense",
			IsSystem: 1, Color: c.color, SortOrder: (i + 1) * 10, CreatedAt: now, UpdatedAt: now}
		if err = s.G.Create(&parent).Error; err != nil {
			return fmt.Errorf("种子分类 %s 失败: %w", c.name, err)
		}
		for j, sub := range c.subs {
			child := Category{ID: fmt.Sprintf("%s_s%d", c.id, j+1), Name: sub, ParentID: &c.id,
				Level: 2, TransactionType: "expense", IsSystem: 1, Color: c.color,
				SortOrder: (j + 1) * 10, CreatedAt: now, UpdatedAt: now}
			if err = s.G.Create(&child).Error; err != nil {
				return fmt.Errorf("种子二级分类 %s 失败: %w", sub, err)
			}
		}
	}
	for i, c := range incomeCats {
		cat := Category{ID: c.id, Name: c.name, Level: 1, TransactionType: "income",
			IsSystem: 1, Color: c.color, SortOrder: (i + 1) * 10, CreatedAt: now, UpdatedAt: now}
		if err = s.G.Create(&cat).Error; err != nil {
			return fmt.Errorf("种子收入分类 %s 失败: %w", c.name, err)
		}
	}

	// ---- 标签（12 个）----
	tagNames := []string{"报销", "可报销", "必要", "非必要", "大额", "冲动消费",
		"出差", "旅行", "家庭", "宠物", "学习", "健康"}
	tagIDs := make([]string, 0, len(tagNames))
	for i, name := range tagNames {
		id := fmt.Sprintf("tag_t%02d", i+1)
		tagIDs = append(tagIDs, id)
		t := Tag{ID: id, Name: name, SortOrder: (i + 1) * 10, CreatedAt: now, UpdatedAt: now}
		if err = s.G.Create(&t).Error; err != nil {
			return fmt.Errorf("种子标签 %s 失败: %w", name, err)
		}
	}

	// ---- 线上商户（10 个）----
	type mchSeed struct{ name, code string }
	merchants := []mchSeed{
		{"淘宝", "taobao"}, {"京东", "jd"}, {"拼多多", "pdd"}, {"美团", "meituan"},
		{"饿了么", "eleme"}, {"滴滴", "didi"}, {"微信", "wechat"}, {"支付宝", "alipay"},
		{"抖音", "douyin"}, {"盒马", "hema"},
	}
	for i, m := range merchants {
		rec := Merchant{ID: fmt.Sprintf("mch_m%02d", i+1), Name: m.name, MerchantType: "online",
			PlatformCode: m.code, IsSystem: 1, CreatedAt: now, UpdatedAt: now}
		if err = s.G.Create(&rec).Error; err != nil {
			return fmt.Errorf("种子商户 %s 失败: %w", m.name, err)
		}
	}

	// ---- 账户（v1.2：只有一个「总账户」，需求 R5）----
	// v1.0/v1.1 的 3 个种子账户（招商/支付宝/微信）不再创建；
	// 老库的多账户由 service.AccountMergeService 在首次启动时自动合并（见 app.New）。
	accounts := []Account{
		{ID: TotalAccountID, Name: TotalAccountName, AccountType: "asset", ValueType: "wallet",
			IsCountInTotal: 1, SortOrder: 10, CreatedAt: now, UpdatedAt: now,
			Remark: "v1.2 起只保留一个总账户"},
	}
	for i := range accounts {
		if err = s.G.Create(&accounts[i]).Error; err != nil {
			return fmt.Errorf("种子账户 %s 失败: %w", accounts[i].Name, err)
		}
	}

	// ---- 账本（3 个，日常为默认）----
	daily := Ledger{ID: "ldg_daily01", Name: "日常", IsDefault: 1, SortOrder: 10,
		CreatedAt: now, UpdatedAt: now}
	travel := Ledger{ID: "ldg_travel1", Name: "旅行", SortOrder: 20, CreatedAt: now, UpdatedAt: now}
	biz := Ledger{ID: "ldg_biz0001", Name: "出差", SortOrder: 30, CreatedAt: now, UpdatedAt: now}
	for _, l := range []Ledger{daily, travel, biz} {
		if err = s.G.Create(&l).Error; err != nil {
			return fmt.Errorf("种子账本 %s 失败: %w", l.Name, err)
		}
	}
	// 日常：全部一级分类 or 全部标签
	dailyRule := LedgerRule{ID: "rule_daily01", LedgerID: daily.ID, MatchMode: "or",
		IsActive: 1, CreatedAt: now, UpdatedAt: now}
	if err = s.G.Create(&dailyRule).Error; err != nil {
		return err
	}
	for _, c := range expenseCats {
		if err = s.G.Create(&LedgerRuleTarget{RuleID: dailyRule.ID, TargetType: "category_l1", TargetID: c.id}).Error; err != nil {
			return err
		}
	}
	for _, c := range incomeCats {
		if err = s.G.Create(&LedgerRuleTarget{RuleID: dailyRule.ID, TargetType: "category_l1", TargetID: c.id}).Error; err != nil {
			return err
		}
	}
	for _, tid := range tagIDs {
		if err = s.G.Create(&LedgerRuleTarget{RuleID: dailyRule.ID, TargetType: "tag", TargetID: tid}).Error; err != nil {
			return err
		}
	}
	// 旅行：标签=旅行；出差：标签=出差
	for _, pair := range [][2]string{{travel.ID, "tag_t08"}, {biz.ID, "tag_t07"}} {
		rule := LedgerRule{ID: "rule_" + pair[0][4:], LedgerID: pair[0], MatchMode: "or",
			IsActive: 1, CreatedAt: now, UpdatedAt: now}
		if err = s.G.Create(&rule).Error; err != nil {
			return err
		}
		if err = s.G.Create(&LedgerRuleTarget{RuleID: rule.ID, TargetType: "tag", TargetID: pair[1]}).Error; err != nil {
			return err
		}
	}

	// ---- meta 默认引用 ----
	for k, v := range map[string]string{
		"default_account_id":          accounts[0].ID,
		"default_ledger_id":           daily.ID,
		"default_category_expense_id": "cat_e01",
		"default_category_income_id":  "cat_i01",
	} {
		if err = meta.Set(k, v); err != nil {
			return err
		}
	}
	return nil
}

// seedRegions 幂等导入地区字典（INSERT OR IGNORE）。
func (s *Store) seedRegions() error {
	var regions []Region
	if err := json.Unmarshal(regionsSeedJSON, &regions); err != nil {
		return fmt.Errorf("解析 regions_seed.json 失败: %w", err)
	}
	for _, r := range regions {
		if err := s.G.Where("code = ?", r.Code).FirstOrCreate(&r).Error; err != nil {
			return fmt.Errorf("导入地区 %s 失败: %w", r.Code, err)
		}
	}
	return nil
}

// v1.2 总账户常量（唯一账户，需求 R5）。
const (
	// TotalAccountID 全新库与合并后的总账户 ID（19 位 ID 规则不适用于账户，v1.0 即用短 ID）。
	TotalAccountID = "acc_total01"
	// TotalAccountName 总账户名称；合并迁移按名称复用或新建。
	TotalAccountName = "总账户"
)
