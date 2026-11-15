package http

import (
	"ledger/internal/pkg/timeutil"
	"ledger/internal/service"
	"ledger/internal/store"
)

// Ref 通用 {id,name} 引用。
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TagDTO 标签展示。
type TagDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// RegionRef 地区引用。
type RegionRef struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// TransactionItemDTO 明细行。
type TransactionItemDTO struct {
	ID                  int64   `json:"id"`
	Name                string  `json:"name"`
	SubCategoryID       *string `json:"sub_category_id"`
	OriginalAmountCents int64   `json:"original_amount_cents"`
	PaidAmountCents     int64   `json:"paid_amount_cents"`
	DiscountCents       int64   `json:"discount_cents"`
	DiscountReason      string  `json:"discount_reason"`
	SortOrder           int     `json:"sort_order"`
}

// TransactionDTO 交易响应（§4.3）。
type TransactionDTO struct {
	ID                   string     `json:"id"`
	CreatedSeq           int64      `json:"created_seq"`
	TransactionTime      string     `json:"transaction_time"`
	LocalDate            string     `json:"local_date"`
	TransactionType      string     `json:"transaction_type"`
	AmountCents          int64      `json:"amount_cents"`
	SignedCents          int64      `json:"signed_cents"`
	Currency             string     `json:"currency"`
	Account              Ref        `json:"account"`
	TransferDirection    *string    `json:"transfer_direction"`
	TransferGroupID      *string    `json:"transfer_group_id"`
	CounterAccount       *Ref       `json:"counter_account"`
	Category             *Ref       `json:"category"`
	SubCategory          *Ref       `json:"sub_category"`
	Merchant             *Ref       `json:"merchant"`
	MerchantText         string     `json:"merchant_text"`
	Region               *RegionRef `json:"region"`
	TagIDs               []string   `json:"tag_ids"`
	Tags                 []TagDTO   `json:"tags"`
	CounterpartyName     string     `json:"counterparty_name"`
	CounterpartyAccount  string     `json:"counterparty_account"`
	Remark               string     `json:"remark"`
	IsAdjustment         int        `json:"is_adjustment"`
	AdjustmentType       *string    `json:"adjustment_type"`
	RelatedTransactionID *string    `json:"related_transaction_id"`
	AccountBalanceAfter  *int64     `json:"account_balance_after"`
	TotalBalanceAfter    *int64     `json:"total_balance_after"`
	IsFrozen             bool       `json:"is_frozen"`
	// v1.2 §9：待确认冻结（导入且早于边界、用户尚未确认）。此类行可写、不展示历史余额。
	FreezePending     bool                 `json:"freeze_pending"`
	FreezeConfirmedAt *int64               `json:"freeze_confirmed_at"`
	ImportBatchID     string               `json:"import_batch_id"`
	IsMaterialized    bool                 `json:"is_materialized"`
	IsDeleted         bool                 `json:"is_deleted"`
	DeletedAt         *int64               `json:"deleted_at"`
	Source            string               `json:"source"`
	TimePrecision     string               `json:"time_precision"`
	ItemCount         int                  `json:"item_count"`
	Items             []TransactionItemDTO `json:"items,omitempty"`
	CreatedAt         int64                `json:"created_at"`
	UpdatedAt         int64                `json:"updated_at"`
}

// dtoTimePrecision 时间精度兜底（老数据/空值 → 'second'，§3.4）。
func dtoTimePrecision(v string) string {
	if v == "day" {
		return "day"
	}
	return "second"
}

// nameCatalog 主数据名字目录（每请求加载一次，避免 N+1）。
type nameCatalog struct {
	accounts   map[string]string
	categories map[string]string
	merchants  map[string]string
	regions    map[string]store.Region
	tags       map[string]store.Tag
}

func loadCatalog(master *store.MasterRepo) (*nameCatalog, error) {
	c := &nameCatalog{
		accounts:   map[string]string{},
		categories: map[string]string{},
		merchants:  map[string]string{},
		regions:    map[string]store.Region{},
		tags:       map[string]store.Tag{},
	}
	accs, err := master.ListAccounts(true)
	if err != nil {
		return nil, err
	}
	for _, a := range accs {
		c.accounts[a.ID] = a.Name
	}
	cats, err := master.ListCategories("", true)
	if err != nil {
		return nil, err
	}
	for _, cat := range cats {
		c.categories[cat.ID] = cat.Name
	}
	mchs, err := master.ListMerchants("", "", 0)
	if err != nil {
		return nil, err
	}
	for _, m := range mchs {
		c.merchants[m.ID] = m.Name
	}
	tags, err := master.ListTags(true, "")
	if err != nil {
		return nil, err
	}
	for _, t := range tags {
		c.tags[t.ID] = t
	}
	return c, nil
}

func (c *nameCatalog) accountRef(id string) Ref {
	return Ref{ID: id, Name: c.accounts[id]}
}

func (c *nameCatalog) refOrNil(id *string, names map[string]string) *Ref {
	if id == nil || *id == "" {
		return nil
	}
	return &Ref{ID: *id, Name: names[*id]}
}

// regionRef 惰性查询地区（region 表按 code 索引，查询便宜）。
func (c *nameCatalog) regionRef(code *string, master *store.MasterRepo) *RegionRef {
	if code == nil || *code == "" {
		return nil
	}
	if rg, ok := c.regions[*code]; ok {
		return &RegionRef{Code: rg.Code, Name: rg.FullName}
	}
	rg, err := master.GetRegion(*code)
	if err != nil {
		return &RegionRef{Code: *code, Name: ""}
	}
	c.regions[*code] = *rg
	return &RegionRef{Code: rg.Code, Name: rg.FullName}
}

// txDTOParams 组装一笔交易 DTO 所需的上下文。
type txDTOParams struct {
	catalog        *nameCatalog
	master         *store.MasterRepo
	tagIDs         map[string][]string
	items          map[string][]store.TransactionItem
	window         map[string]store.WindowBalance
	lastFrozenAt   int64
	freezeBoundary int64
	withItems      bool
}

// toTransactionDTO 模型 → DTO（含时区转换与动态区窗口余额填充，§6.5 DTO 填充规则）。
func toTransactionDTO(t *store.Transaction, p *txDTOParams) TransactionDTO {
	dto := TransactionDTO{
		ID:                   t.ID,
		CreatedSeq:           t.CreatedSeq,
		TransactionTime:      timeutil.FormatLocal(t.TransactionTime),
		LocalDate:            t.LocalDate,
		TransactionType:      t.TransactionType,
		AmountCents:          t.AmountCents,
		SignedCents:          t.SignedCents(),
		Currency:             t.Currency,
		Account:              p.catalog.accountRef(t.AccountID),
		TransferDirection:    t.TransferDirection,
		TransferGroupID:      t.TransferGroupID,
		CounterAccount:       p.catalog.refOrNil(t.CounterAccountID, p.catalog.accounts),
		Category:             p.catalog.refOrNil(t.CategoryID, p.catalog.categories),
		SubCategory:          p.catalog.refOrNil(t.SubCategoryID, p.catalog.categories),
		Merchant:             p.catalog.refOrNil(t.MerchantID, p.catalog.merchants),
		MerchantText:         t.MerchantText,
		Region:               p.catalog.regionRef(t.RegionID, p.master),
		TagIDs:               []string{},
		Tags:                 []TagDTO{},
		CounterpartyName:     t.CounterpartyName,
		CounterpartyAccount:  t.CounterpartyAccount,
		Remark:               t.Remark,
		IsAdjustment:         t.IsAdjustment,
		AdjustmentType:       t.AdjustmentType,
		RelatedTransactionID: t.RelatedTransactionID,
		AccountBalanceAfter:  t.AccountBalanceAfter,
		TotalBalanceAfter:    t.TotalBalanceAfter,
		IsDeleted:            t.DeletedAt != nil,
		DeletedAt:            t.DeletedAt,
		Source:               t.Source,
		TimePrecision:        dtoTimePrecision(t.TimePrecision),
		CreatedAt:            t.CreatedAt,
		UpdatedAt:            t.UpdatedAt,
	}
	// 冻结/物化标志（§9.3 #6：唯一判定收敛到 IsWriteProtected）
	dto.IsFrozen = service.IsWriteProtected(t, p.freezeBoundary)
	dto.FreezePending = service.IsFreezePending(t)
	dto.FreezeConfirmedAt = t.FreezeConfirmedAt
	dto.ImportBatchID = t.ImportBatchID
	// 是否已物化：直接看库里有没有物化值（§9.3 第 5 条，团队已定）。
	// 与旧写法 `time < last_frozen_at` 在当前钳制逻辑下等价，但语义更强且少一个分支：
	// 物化区间是左闭右开 [last_frozen_at, target)，恰好 == last 的行本来就是动态行；
	// 待确认行（freeze_confirmed=0）永远没有物化值，因此天然为 false。
	// 不要改回按时间比较 —— 以后调整物化逻辑时这里会悄悄错。
	dto.IsMaterialized = t.AccountBalanceAfter != nil
	// 动态区余额：t >= last_frozen_at → 用窗口映射
	if !dto.IsMaterialized && p.window != nil {
		if wb, ok := p.window[t.ID]; ok {
			accBal := wb.AccountBalanceAfter
			totBal := wb.TotalBalanceAfter
			dto.AccountBalanceAfter = &accBal
			dto.TotalBalanceAfter = &totBal
		}
	}
	// 标签与明细
	if ids, ok := p.tagIDs[t.ID]; ok {
		for _, tid := range ids {
			dto.TagIDs = append(dto.TagIDs, tid)
			if tag, ok2 := p.catalog.tags[tid]; ok2 {
				dto.Tags = append(dto.Tags, TagDTO{ID: tag.ID, Name: tag.Name, Color: tag.Color})
			}
		}
	}
	if items, ok := p.items[t.ID]; ok {
		dto.ItemCount = len(items)
		if p.withItems {
			for _, it := range items {
				dto.Items = append(dto.Items, TransactionItemDTO{
					ID: it.ID, Name: it.Name, SubCategoryID: it.SubCategoryID,
					OriginalAmountCents: it.OriginalAmountCents,
					PaidAmountCents:     it.PaidAmountCents,
					DiscountCents:       it.DiscountCents,
					DiscountReason:      it.DiscountReason,
					SortOrder:           it.SortOrder,
				})
			}
		}
	}
	return dto
}

// AccountDTO 账户响应（§5.2）。
type AccountDTO struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Alias               string `json:"alias"`
	DisplayName         string `json:"display_name"`
	AccountType         string `json:"account_type"`
	ValueType           string `json:"value_type"`
	BankName            string `json:"bank_name"`
	CardTail            string `json:"card_tail"`
	Currency            string `json:"currency"`
	InitialBalanceCents int64  `json:"initial_balance_cents"`
	InitialBalanceTime  string `json:"initial_balance_time"`
	IsCountInTotal      int    `json:"is_count_in_total"`
	BalanceCents        *int64 `json:"balance_cents"`
	SortOrder           int    `json:"sort_order"`
	IsArchived          int    `json:"is_archived"`
	Remark              string `json:"remark"`
	LastTransactionTime string `json:"last_transaction_time,omitempty"`
	TransactionCount    *int64 `json:"transaction_count,omitempty"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

// DisplayName 名称（卡号后四位）。
func DisplayName(a *store.Account) string {
	if a.CardTail != "" {
		return a.Name + "（" + a.CardTail + "）"
	}
	return a.Name
}

func toAccountDTO(a *store.Account) AccountDTO {
	return AccountDTO{
		ID: a.ID, Name: a.Name, Alias: a.Alias, DisplayName: DisplayName(a),
		AccountType: a.AccountType, ValueType: a.ValueType, BankName: a.BankName,
		CardTail: a.CardTail, Currency: a.Currency,
		InitialBalanceCents: a.InitialBalanceCents,
		InitialBalanceTime:  timeutil.FormatLocal(a.InitialBalanceTime),
		IsCountInTotal:      a.IsCountInTotal,
		SortOrder:           a.SortOrder, IsArchived: a.IsArchived, Remark: a.Remark,
		CreatedAt: timeutil.FormatLocal(a.CreatedAt), UpdatedAt: timeutil.FormatLocal(a.UpdatedAt),
	}
}

// CategoryDTO 分类响应（tree 模式 children 嵌套）。
type CategoryDTO struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	ParentID        *string       `json:"parent_id"`
	Level           int           `json:"level"`
	TransactionType string        `json:"transaction_type"`
	Color           string        `json:"color"`
	Icon            string        `json:"icon"`
	SortOrder       int           `json:"sort_order"`
	IsSystem        int           `json:"is_system"`
	IsArchived      int           `json:"is_archived"`
	Children        []CategoryDTO `json:"children"`
}

func toCategoryDTO(c *store.Category) CategoryDTO {
	return CategoryDTO{
		ID: c.ID, Name: c.Name, ParentID: c.ParentID, Level: c.Level,
		TransactionType: c.TransactionType, Color: c.Color, Icon: c.Icon,
		SortOrder: c.SortOrder, IsSystem: c.IsSystem, IsArchived: c.IsArchived,
		Children: []CategoryDTO{},
	}
}

// TagRespDTO 标签响应。
type TagRespDTO struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	UseCount   int    `json:"use_count"`
	SortOrder  int    `json:"sort_order"`
	IsArchived int    `json:"is_archived"`
}

// MerchantDTO 商户响应。
type MerchantDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Alias        string `json:"alias"`
	MerchantType string `json:"merchant_type"`
	PlatformCode string `json:"platform_code"`
	RegionID     string `json:"region_id"`
	UseCount     int    `json:"use_count"`
	IsSystem     int    `json:"is_system"`
	IsArchived   int    `json:"is_archived"`
}

// RegionDTO 地区响应。
type RegionDTO struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	FullName   string `json:"full_name"`
	ParentCode string `json:"parent_code"`
	Level      int    `json:"level"`
	Pinyin     string `json:"pinyin"`
	PinyinAbbr string `json:"pinyin_abbr"`
}

// LedgerRuleTargetDTO 规则目标。
type LedgerRuleTargetDTO struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
}

// LedgerRuleDTO 规则。
type LedgerRuleDTO struct {
	ID        string                `json:"id"`
	MatchMode string                `json:"match_mode"`
	Targets   []LedgerRuleTargetDTO `json:"targets"`
}

// LedgerDTO 账本响应（§5.3）。
type LedgerDTO struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Currency         string          `json:"currency"`
	Remark           string          `json:"remark"`
	IsDefault        int             `json:"is_default"`
	SortOrder        int             `json:"sort_order"`
	IsArchived       int             `json:"is_archived"`
	TransactionCount *int64          `json:"transaction_count,omitempty"`
	Rules            []LedgerRuleDTO `json:"rules"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
}
