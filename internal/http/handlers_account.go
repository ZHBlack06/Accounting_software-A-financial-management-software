package http

import (
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/ids"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/service"
	"ledger/internal/store"
)

// AccountHandler 账户处理器。
type AccountHandler struct {
	s       *store.Store
	master  *store.MasterRepo
	views   *store.ViewManager
	balance *service.BalanceService
	mat     *service.Materializer
	merge   *service.AccountMergeService
}

// NewAccountHandler 构造。
func NewAccountHandler(s *store.Store, master *store.MasterRepo, views *store.ViewManager,
	balance *service.BalanceService, mat *service.Materializer,
	merge *service.AccountMergeService) *AccountHandler {
	return &AccountHandler{s: s, master: master, views: views, balance: balance, mat: mat, merge: merge}
}

var cardTailRe = regexp.MustCompile(`^\d{4}$`)

var validValueTypes = map[string]bool{
	"wallet": true, "wealth": true, "yuebao": true, "current": true, "fixed": true, "other": true,
}

// List GET /api/accounts?include_archived=false&with_balance=true
func (h *AccountHandler) List(c *gin.Context) {
	includeArchived := c.Query("include_archived") == "true"
	withBalance := c.Query("with_balance") == "true"
	accs, err := h.master.ListAccounts(includeArchived)
	if err != nil {
		Err(c, err)
		return
	}
	var balances *service.Balances
	if withBalance {
		if balances, err = h.balance.Current(c.Request.Context()); err != nil {
			Err(c, err)
			return
		}
	}
	items := make([]AccountDTO, 0, len(accs))
	for i := range accs {
		dto := toAccountDTO(&accs[i])
		if withBalance {
			b := balances.ByAccount[accs[i].ID]
			dto.BalanceCents = &b
		}
		items = append(items, dto)
	}
	resp := gin.H{
		"total":     len(items),
		"page":      1,
		"page_size": len(items),
		"items":     items,
	}
	if withBalance {
		resp["total_balance_cents"] = balances.Total
	}
	OK(c, resp)
}

type accountUpsertBody struct {
	Name                string `json:"name"`
	Alias               string `json:"alias"`
	ValueType           string `json:"value_type"`
	BankName            string `json:"bank_name"`
	CardTail            string `json:"card_tail"`
	Currency            string `json:"currency"`
	InitialBalanceCents *int64 `json:"initial_balance_cents"`
	InitialBalanceTime  string `json:"initial_balance_time"`
	IsCountInTotal      *int   `json:"is_count_in_total"`
	SortOrder           *int   `json:"sort_order"`
	Remark              string `json:"remark"`
}

func validateAccountBody(c *gin.Context, b *accountUpsertBody) bool {
	if b.Name == "" {
		BadRequest(c, "name 必填")
		return false
	}
	if b.CardTail != "" && !cardTailRe.MatchString(b.CardTail) {
		BadRequest(c, "card_tail 必须为 4 位数字")
		return false
	}
	if b.ValueType != "" && !validValueTypes[b.ValueType] {
		BadRequest(c, "value_type 非法")
		return false
	}
	return true
}

// Create POST /api/accounts（同时建视图）。
func (h *AccountHandler) Create(c *gin.Context) {
	var b accountUpsertBody
	if err := c.ShouldBindJSON(&b); err != nil {
		BadRequest(c, "请求体非法: "+err.Error())
		return
	}
	if !validateAccountBody(c, &b) {
		return
	}
	now := timeutil.NowUnix()
	acc := store.Account{
		ID: ids.ShortID("acc_"), Name: b.Name, Alias: b.Alias,
		AccountType: "asset", ValueType: nnStr(b.ValueType, "wallet"),
		BankName: b.BankName, CardTail: b.CardTail, Currency: nnStr(b.Currency, "CNY"),
		IsCountInTotal: 1, Remark: b.Remark, CreatedAt: now, UpdatedAt: now,
	}
	if b.InitialBalanceCents != nil {
		acc.InitialBalanceCents = *b.InitialBalanceCents
	}
	if b.InitialBalanceTime != "" {
		t, err := timeutil.ParseTimeOrDate(b.InitialBalanceTime, false)
		if err != nil {
			BadRequest(c, "initial_balance_time 格式非法")
			return
		}
		acc.InitialBalanceTime = t
	}
	if b.IsCountInTotal != nil {
		acc.IsCountInTotal = *b.IsCountInTotal
	}
	if b.SortOrder != nil {
		acc.SortOrder = *b.SortOrder
	}
	if err := h.s.G.Create(&acc).Error; err != nil {
		Err(c, err)
		return
	}
	if err := h.views.CreateAccountView(acc.ID); err != nil {
		Err(c, err)
		return
	}
	Created(c, toAccountDTO(&acc))
}

// Get GET /api/accounts/:id（含余额、交易数、最近交易日）。
func (h *AccountHandler) Get(c *gin.Context) {
	acc, err := h.master.GetAccount(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "账户不存在")
			return
		}
		Err(c, err)
		return
	}
	dto := toAccountDTO(acc)
	balances, err := h.balance.Current(c.Request.Context())
	if err != nil {
		Err(c, err)
		return
	}
	b := balances.ByAccount[acc.ID]
	dto.BalanceCents = &b
	cnt, err := h.master.CountAccountTransactions(acc.ID)
	if err != nil {
		Err(c, err)
		return
	}
	dto.TransactionCount = &cnt
	if last, err := h.master.LastAccountTransactionTime(acc.ID); err == nil && last != nil {
		dto.LastTransactionTime = timeutil.FormatLocal(*last)
	}
	OK(c, dto)
}

// Update PUT /api/accounts/:id。
// initial_balance_cents 仅在冻结区无交易时允许改（T03.5 / PRD 2.13 2-H）。
func (h *AccountHandler) Update(c *gin.Context) {
	acc, err := h.master.GetAccount(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "账户不存在")
			return
		}
		Err(c, err)
		return
	}
	var b accountUpsertBody
	if err := c.ShouldBindJSON(&b); err != nil {
		BadRequest(c, "请求体非法: "+err.Error())
		return
	}
	if b.Name != "" || b.CardTail != "" || b.ValueType != "" {
		if !validateAccountBody(c, &b) {
			return
		}
	}
	initialChanged := false
	if b.InitialBalanceCents != nil && *b.InitialBalanceCents != acc.InitialBalanceCents {
		boundary := h.balance.FreezeBoundary(time.Now().Unix())
		n, err := h.master.CountFrozenAccountTransactions(acc.ID, boundary)
		if err != nil {
			Err(c, err)
			return
		}
		if n > 0 {
			ErrCode(c, apierr.CodeFrozen, "该账户存在冻结区交易，不可修改初始余额", map[string]any{
				"frozen_transaction_count": n,
				"freeze_boundary":          timeutil.FormatLocal(boundary),
			})
			return
		}
		acc.InitialBalanceCents = *b.InitialBalanceCents
		initialChanged = true
	}
	if b.Name != "" {
		acc.Name = b.Name
	}
	acc.Alias = b.Alias
	if b.ValueType != "" {
		acc.ValueType = b.ValueType
	}
	acc.BankName = b.BankName
	if b.CardTail != "" {
		acc.CardTail = b.CardTail
	}
	if b.Currency != "" {
		acc.Currency = b.Currency
	}
	if b.InitialBalanceTime != "" {
		t, err := timeutil.ParseTimeOrDate(b.InitialBalanceTime, false)
		if err != nil {
			BadRequest(c, "initial_balance_time 格式非法")
			return
		}
		acc.InitialBalanceTime = t
	}
	if b.IsCountInTotal != nil {
		acc.IsCountInTotal = *b.IsCountInTotal
	}
	if b.SortOrder != nil {
		acc.SortOrder = *b.SortOrder
	}
	acc.Remark = b.Remark
	if err := h.s.G.Save(acc).Error; err != nil {
		Err(c, err)
		return
	}
	// 初始余额变更会让已物化行的基准失效 → 全量重物化（防御，正常已被冻结校验拦截）
	if initialChanged {
		_, _ = h.mat.ForceFull(c.Request.Context(), time.Now())
	}
	OK(c, toAccountDTO(acc))
}

// Archive POST /api/accounts/:id/archive {archived:true|false}。
func (h *AccountHandler) Archive(c *gin.Context) {
	acc, err := h.master.GetAccount(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "账户不存在")
			return
		}
		Err(c, err)
		return
	}
	var b struct {
		Archived bool `json:"archived"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		BadRequest(c, "请求体非法")
		return
	}
	if b.Archived {
		acc.IsArchived = 1
		now := timeutil.NowUnix()
		acc.ArchivedAt = &now
	} else {
		acc.IsArchived = 0
		acc.ArchivedAt = nil
	}
	if err := h.s.G.Save(acc).Error; err != nil {
		Err(c, err)
		return
	}
	OK(c, toAccountDTO(acc))
}

// Delete DELETE /api/accounts/:id（有交易 → 409 ACCOUNT_IN_USE）。
func (h *AccountHandler) Delete(c *gin.Context) {
	acc, err := h.master.GetAccount(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "账户不存在")
			return
		}
		Err(c, err)
		return
	}
	n, err := h.master.CountAccountTransactions(acc.ID)
	if err != nil {
		Err(c, err)
		return
	}
	if n > 0 {
		ErrCode(c, apierr.CodeAccountInUse, "该账户存在交易，无法删除，可改为归档", map[string]any{
			"transaction_count": n,
		})
		return
	}
	if err := h.s.G.Delete(&store.Account{}, "id = ?", acc.ID).Error; err != nil {
		Err(c, err)
		return
	}
	if err := h.views.DropAccountView(acc.ID); err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"ok": true})
}

// Reorder POST /api/accounts/reorder {items:[{id,sort_order}]}。
func (h *AccountHandler) Reorder(c *gin.Context) {
	var b struct {
		Items []struct {
			ID        string `json:"id"`
			SortOrder int    `json:"sort_order"`
		} `json:"items"`
	}
	if err := c.ShouldBindJSON(&b); err != nil || len(b.Items) == 0 {
		BadRequest(c, "items 必填")
		return
	}
	for _, it := range b.Items {
		if err := h.s.G.Model(&store.Account{}).Where("id = ?", it.ID).
			Update("sort_order", it.SortOrder).Error; err != nil {
			Err(c, err)
			return
		}
	}
	OK(c, gin.H{"ok": true})
}

func nnStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Merge POST /api/accounts/merge（§2.5）：合并账户到「总账户」并全量重算。
func (h *AccountHandler) Merge(c *gin.Context) {
	var in service.MergeRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		BadRequest(c, "请求体非法: "+err.Error())
		return
	}
	if len(in.SourceIDs) == 0 {
		BadRequest(c, "source_ids 不能为空")
		return
	}
	if h.merge == nil {
		Err(c, apierr.New(apierr.CodeInternal, "账户合并服务未初始化"))
		return
	}
	res, err := h.merge.Merge(c.Request.Context(), &in)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}
