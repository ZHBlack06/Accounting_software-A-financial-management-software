package http

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/service"
	"ledger/internal/store"
)

// TransactionHandler 交易处理器（§5.4 端点 35–41）。
type TransactionHandler struct {
	s       *store.Store
	master  *store.MasterRepo
	txRepo  *store.TxRepo
	txn     *service.TxnService
	adjust  *service.AdjustService
	balance *service.BalanceService
	mat     *service.Materializer
	// v1.2 §9：延迟冻结确认（端点 75）
	freeze *service.FreezeService
}

// NewTransactionHandler 构造。
func NewTransactionHandler(s *store.Store, master *store.MasterRepo, txRepo *store.TxRepo,
	txn *service.TxnService, adjust *service.AdjustService,
	balance *service.BalanceService, mat *service.Materializer,
	freeze *service.FreezeService) *TransactionHandler {
	return &TransactionHandler{s: s, master: master, txRepo: txRepo, txn: txn,
		adjust: adjust, balance: balance, mat: mat, freeze: freeze}
}

// buildDTOParams 组装 DTO 上下文（目录 + 标签 + 明细 + 窗口余额）。
func (h *TransactionHandler) buildDTOParams(c *gin.Context, txs []*store.Transaction, withItems bool) (*txDTOParams, error) {
	catalog, err := loadCatalog(h.master)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(txs))
	for _, t := range txs {
		ids = append(ids, t.ID)
	}
	tagIDs, err := h.txRepo.TagIDsFor(ids)
	if err != nil {
		return nil, err
	}
	items, err := h.txRepo.ItemsFor(ids)
	if err != nil {
		return nil, err
	}
	last, err := h.balance.LastFrozenAt()
	if err != nil {
		return nil, err
	}
	window, _, err := h.balance.WindowBalances()
	if err != nil {
		return nil, err
	}
	return &txDTOParams{
		catalog:        catalog,
		master:         h.master,
		tagIDs:         tagIDs,
		items:          items,
		window:         window,
		lastFrozenAt:   last,
		freezeBoundary: h.balance.FreezeBoundary(time.Now().Unix()),
		withItems:      withItems,
	}, nil
}

func toDTOs(txs []*store.Transaction, p *txDTOParams) []TransactionDTO {
	out := make([]TransactionDTO, 0, len(txs))
	for _, t := range txs {
		out = append(out, toTransactionDTO(t, p))
	}
	return out
}

// List GET /api/transactions（端点 35，12+ 过滤参数）。
func (h *TransactionHandler) List(c *gin.Context) {
	// 查询前补偿物化（§6.4）
	h.mat.EnsureFresh(c.Request.Context())

	q := &store.TxQuery{IncludeSub: true, IncludeAdjustments: true}
	start, end, err := store.ParseTimeRange(c.Query("start_time"), c.Query("end_time"))
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	q.StartTime, q.EndTime = start, end

	// account_id 可重复/逗号分隔
	var accIDs []string
	for _, v := range c.QueryArray("account_id") {
		accIDs = append(accIDs, store.SplitCSV(v)...)
	}
	q.AccountIDs = accIDs
	q.CategoryID = c.Query("category_id")
	if v := c.Query("include_sub"); v == "false" {
		q.IncludeSub = false
	}
	q.SubCategoryID = c.Query("sub_category_id")
	q.TagIDs = store.SplitCSV(c.Query("tag_ids"))
	q.TagMatch = c.DefaultQuery("tag_match", "any")
	q.MerchantID = c.Query("merchant_id")
	q.RegionID = c.Query("region_id")
	q.LedgerID = c.Query("ledger_id")
	q.Types = store.SplitCSV(c.Query("type"))
	if v := c.Query("amount_min_cents"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			BadRequest(c, "amount_min_cents 必须为整数")
			return
		}
		q.AmountMinCents = &n
	}
	if v := c.Query("amount_max_cents"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			BadRequest(c, "amount_max_cents 必须为整数")
			return
		}
		q.AmountMaxCents = &n
	}
	if v := c.Query("include_adjustments"); v == "false" {
		q.IncludeAdjustments = false
	}
	q.OnlyAdjustments = c.Query("only_adjustments") == "true"
	q.IncludeDeleted = c.Query("include_deleted") == "true"
	// v1.2 §9.8：freeze_pending 过滤（1|true 只看待确认；0|false 只看已确认；缺省不过滤）
	if v := strings.TrimSpace(c.Query("freeze_pending")); v != "" {
		var want bool
		switch strings.ToLower(v) {
		case "1", "true", "yes":
			want = true
		case "0", "false", "no":
			want = false
		default:
			BadRequest(c, "freeze_pending 必须为 1|0|true|false")
			return
		}
		q.FreezePending = &want
	}
	q.Keyword = c.Query("keyword")
	q.Sort = c.DefaultQuery("sort", "time_desc")
	if v := c.Query("page"); v != "" {
		n, _ := strconv.Atoi(v)
		q.Page = n
	}
	if v := c.Query("page_size"); v != "" {
		n, _ := strconv.Atoi(v)
		q.PageSize = n
	}

	items, total, summary, err := h.txRepo.ListTransactions(q, h.master.ChildCategoryIDs)
	if err != nil {
		Err(c, err)
		return
	}
	params, err := h.buildDTOParams(c, items, false)
	if err != nil {
		Err(c, err)
		return
	}
	page := q.Page
	if page < 1 {
		page = 1
	}
	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	OK(c, gin.H{
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"summary":   summary,
		"items":     toDTOs(items, params),
	})
}

type txnItemBody struct {
	Name                string  `json:"name"`
	SubCategoryID       *string `json:"sub_category_id"`
	OriginalAmountCents int64   `json:"original_amount_cents"`
	PaidAmountCents     int64   `json:"paid_amount_cents"`
	DiscountReason      string  `json:"discount_reason"`
	SortOrder           int     `json:"sort_order"`
}

type createTxnBody struct {
	TransactionTime     string        `json:"transaction_time"`
	TransactionType     string        `json:"transaction_type"`
	AmountCents         int64         `json:"amount_cents"`
	Currency            string        `json:"currency"`
	AccountID           string        `json:"account_id"`
	CategoryID          *string       `json:"category_id"`
	SubCategoryID       *string       `json:"sub_category_id"`
	MerchantID          *string       `json:"merchant_id"`
	MerchantText        string        `json:"merchant_text"`
	RegionID            *string       `json:"region_id"`
	CounterpartyName    string        `json:"counterparty_name"`
	CounterpartyAccount string        `json:"counterparty_account"`
	TagIDs              []string      `json:"tag_ids"`
	Remark              string        `json:"remark"`
	Source              string        `json:"source"`
	ExternalID          string        `json:"external_id"`
	Items               []txnItemBody `json:"items"`
	Transfer            *struct {
		FromAccountID string `json:"from_account_id"`
		ToAccountID   string `json:"to_account_id"`
	} `json:"transfer"`
	AllowFrozen     bool   `json:"allow_frozen"`
	AllowDuplicate  bool   `json:"allow_duplicate"`
	ClientRequestID string `json:"client_request_id"`
}

// balancesBlock 创建后的账户/总余额。
func (h *TransactionHandler) balancesBlock(c *gin.Context, accountID string) (gin.H, error) {
	balances, err := h.balance.Current(c.Request.Context())
	if err != nil {
		return nil, err
	}
	return gin.H{
		"account_balance_cents": balances.ByAccount[accountID],
		"total_balance_cents":   balances.Total,
	}, nil
}

// Create POST /api/transactions（端点 36）。
func (h *TransactionHandler) Create(c *gin.Context) {
	var b createTxnBody
	if err := c.ShouldBindJSON(&b); err != nil {
		BadRequest(c, "请求体非法: "+err.Error())
		return
	}
	in := &service.CreateTxnInput{
		TransactionType: b.TransactionType, AmountCents: b.AmountCents,
		Currency: b.Currency, AccountID: b.AccountID,
		CategoryID: b.CategoryID, SubCategoryID: b.SubCategoryID,
		MerchantID: b.MerchantID, MerchantText: b.MerchantText, RegionID: b.RegionID,
		CounterpartyName: b.CounterpartyName, CounterpartyAccount: b.CounterpartyAccount,
		TagIDs: b.TagIDs, Remark: b.Remark, Source: b.Source, ExternalID: b.ExternalID,
		AllowFrozen: b.AllowFrozen, AllowDuplicate: b.AllowDuplicate,
	}
	if b.TransactionTime != "" {
		t, err := timeutil.ParseTimeOrDate(b.TransactionTime, false)
		if err != nil {
			BadRequest(c, "transaction_time 格式非法，应为 YYYY-MM-DD HH:mm:ss")
			return
		}
		in.TransactionTime = &t
	}
	if b.Transfer != nil {
		in.Transfer = &service.TransferInput{
			FromAccountID: b.Transfer.FromAccountID,
			ToAccountID:   b.Transfer.ToAccountID,
		}
	}
	for _, it := range b.Items {
		in.Items = append(in.Items, service.TxnItemInput{
			Name: it.Name, SubCategoryID: it.SubCategoryID,
			OriginalAmountCents: it.OriginalAmountCents,
			PaidAmountCents:     it.PaidAmountCents,
			DiscountReason:      it.DiscountReason,
			SortOrder:           it.SortOrder,
		})
	}
	res, err := h.txn.Create(c.Request.Context(), in)
	if err != nil {
		Err(c, err)
		return
	}
	all := append([]*store.Transaction{res.Primary}, res.Linked...)
	params, err := h.buildDTOParams(c, all, true)
	if err != nil {
		Err(c, err)
		return
	}
	primary := toTransactionDTO(res.Primary, params)
	linked := toDTOs(res.Linked, params)
	if linked == nil {
		linked = []TransactionDTO{}
	}
	balances, err := h.balancesBlock(c, res.Primary.AccountID)
	if err != nil {
		Err(c, err)
		return
	}
	Created(c, gin.H{
		"transaction": primary,
		"linked":      linked,
		"balances":    balances,
		"warning":     res.Warning,
	})
}

// Get GET /api/transactions/:id（端点 37：transaction + linked + adjustments + screenshot）。
func (h *TransactionHandler) Get(c *gin.Context) {
	t, err := h.txn.Get(c.Param("id"))
	if err != nil {
		Err(c, err)
		return
	}
	related := []*store.Transaction{t}
	var linked []*store.Transaction
	if t.TransferGroupID != nil {
		group, err := h.txRepo.GetByTransferGroup(*t.TransferGroupID)
		if err != nil {
			Err(c, err)
			return
		}
		for _, g := range group {
			if g.ID != t.ID {
				linked = append(linked, g)
			}
		}
		related = append(related, linked...)
	}
	adjustments, err := h.txRepo.GetAdjustments(t.ID)
	if err != nil {
		Err(c, err)
		return
	}
	related = append(related, adjustments...)
	params, err := h.buildDTOParams(c, related, true)
	if err != nil {
		Err(c, err)
		return
	}
	linkedDTOs := toDTOs(linked, params)
	if linkedDTOs == nil {
		linkedDTOs = []TransactionDTO{}
	}
	adjDTOs := toDTOs(adjustments, params)
	if adjDTOs == nil {
		adjDTOs = []TransactionDTO{}
	}
	var screenshot any // T08 填充
	OK(c, gin.H{
		"transaction": toTransactionDTO(t, params),
		"linked":      linkedDTOs,
		"adjustments": adjDTOs,
		"screenshot":  screenshot,
	})
}

// nullableStr 解析可空字符串字段：key 存在且为 null → (指向 nil 的指针)；
// key 存在且有值 → (**string)；key 不存在 → nil（不更新）。
func nullableStr(raw map[string]json.RawMessage, key string) **string {
	v, ok := raw[key]
	if !ok {
		return nil
	}
	if string(v) == "null" {
		nilStr := (*string)(nil)
		return &nilStr
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return nil
	}
	sp := &s
	return &sp
}

// Update PUT /api/transactions/:id（端点 38，仅动态区）。
func (h *TransactionHandler) Update(c *gin.Context) {
	var raw map[string]json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		BadRequest(c, "请求体非法: "+err.Error())
		return
	}
	in := &service.UpdateTxnInput{
		CategoryID:    nullableStr(raw, "category_id"),
		SubCategoryID: nullableStr(raw, "sub_category_id"),
		MerchantID:    nullableStr(raw, "merchant_id"),
		RegionID:      nullableStr(raw, "region_id"),
	}
	if v, ok := raw["transaction_time"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			BadRequest(c, "transaction_time 必须为字符串")
			return
		}
		t, err := timeutil.ParseTimeOrDate(s, false)
		if err != nil {
			BadRequest(c, "transaction_time 格式非法")
			return
		}
		in.TransactionTime = &t
	}
	if v, ok := raw["amount_cents"]; ok {
		var n int64
		if err := json.Unmarshal(v, &n); err != nil {
			BadRequest(c, "amount_cents 必须为整数")
			return
		}
		in.AmountCents = &n
	}
	if v, ok := raw["account_id"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			BadRequest(c, "account_id 必须为字符串")
			return
		}
		in.AccountID = &s
	}
	for _, pair := range []struct {
		key string
		dst **string
	}{
		{"merchant_text", &in.MerchantText},
		{"counterparty_name", &in.CounterpartyName},
		{"counterparty_account", &in.CounterpartyAccount},
		{"remark", &in.Remark},
	} {
		if v, ok := raw[pair.key]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				BadRequest(c, pair.key+" 必须为字符串")
				return
			}
			*pair.dst = &s
		}
	}
	if v, ok := raw["tag_ids"]; ok {
		var ids []string
		if err := json.Unmarshal(v, &ids); err != nil {
			BadRequest(c, "tag_ids 必须为数组")
			return
		}
		in.TagIDs = &ids
	}
	if v, ok := raw["items"]; ok {
		var items []txnItemBody
		if err := json.Unmarshal(v, &items); err != nil {
			BadRequest(c, "items 必须为数组")
			return
		}
		list := make([]service.TxnItemInput, 0, len(items))
		for _, it := range items {
			list = append(list, service.TxnItemInput{
				Name: it.Name, SubCategoryID: it.SubCategoryID,
				OriginalAmountCents: it.OriginalAmountCents,
				PaidAmountCents:     it.PaidAmountCents,
				DiscountReason:      it.DiscountReason,
				SortOrder:           it.SortOrder,
			})
		}
		in.Items = &list
	}
	t, err := h.txn.Update(c.Request.Context(), c.Param("id"), in)
	if err != nil {
		Err(c, err)
		return
	}
	params, err := h.buildDTOParams(c, []*store.Transaction{t}, true)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"transaction": toTransactionDTO(t, params)})
}

// Adjust POST /api/transactions/:id/adjust（端点 42，§6.7）。
func (h *TransactionHandler) Adjust(c *gin.Context) {
	var b struct {
		Mode             string  `json:"mode"`
		NewAmountCents   int64   `json:"new_amount_cents"`
		NewCategoryID    *string `json:"new_category_id"`
		NewSubCategoryID *string `json:"new_sub_category_id"`
		NewMerchantID    *string `json:"new_merchant_id"`
		NewMerchantText  *string `json:"new_merchant_text"`
		Remark           string  `json:"remark"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		BadRequest(c, "请求体非法: "+err.Error())
		return
	}
	res, err := h.adjust.Adjust(c.Request.Context(), c.Param("id"), &service.AdjustInput{
		Mode: b.Mode, NewAmountCents: b.NewAmountCents,
		NewCategoryID: b.NewCategoryID, NewSubCategoryID: b.NewSubCategoryID,
		NewMerchantID: b.NewMerchantID, NewMerchantText: b.NewMerchantText,
		Remark: b.Remark,
	})
	if err != nil {
		Err(c, err)
		return
	}
	params, err := h.buildDTOParams(c, res.Created, true)
	if err != nil {
		Err(c, err)
		return
	}
	balances, err := h.balancesBlock(c, res.Created[0].AccountID)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{
		"created":     toDTOs(res.Created, params),
		"delta_cents": res.DeltaCents,
		"balances":    balances,
		"note":        "原交易若在冻结区，报表历史区间仍显示原金额",
	})
}

// BatchDelete POST /api/transactions/batch-delete（端点 43）。
func (h *TransactionHandler) BatchDelete(c *gin.Context) {
	var b struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&b); err != nil || len(b.IDs) == 0 {
		BadRequest(c, "ids 必填")
		return
	}
	succeeded := []string{}
	failed := []gin.H{}
	for _, id := range b.IDs {
		if err := h.txn.SoftDelete(c.Request.Context(), id, true); err != nil {
			code := "INTERNAL"
			if ae, ok := err.(*apierr.Error); ok {
				code = ae.Code
			}
			failed = append(failed, gin.H{"id": id, "code": code})
			continue
		}
		succeeded = append(succeeded, id)
	}
	OK(c, gin.H{"succeeded": succeeded, "failed": failed})
}

// Delete DELETE /api/transactions/:id?with_transfer=true（端点 39，软删除）。
func (h *TransactionHandler) Delete(c *gin.Context) {
	withTransfer := true
	if v := strings.ToLower(c.Query("with_transfer")); v == "false" {
		withTransfer = false
	}
	if err := h.txn.SoftDelete(c.Request.Context(), c.Param("id"), withTransfer); err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"ok": true})
}

// Restore POST /api/transactions/:id/restore（端点 40）。
func (h *TransactionHandler) Restore(c *gin.Context) {
	if err := h.txn.Restore(c.Request.Context(), c.Param("id")); err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"ok": true})
}

// Purge DELETE /api/transactions/:id/purge（端点 41，软删满 180 天）。
func (h *TransactionHandler) Purge(c *gin.Context) {
	if err := h.txn.Purge(c.Request.Context(), c.Param("id")); err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"ok": true})
}

// FreezeConfirm POST /api/transactions/freeze-confirm（端点 75，§9.6）。
// 三选一：transaction_ids（≤5000）/ batch_id / all=true（需 confirm=true）。
func (h *TransactionHandler) FreezeConfirm(c *gin.Context) {
	var in service.FreezeConfirmInput
	if err := c.ShouldBindJSON(&in); err != nil {
		BadRequest(c, "请求体非法: "+err.Error())
		return
	}
	if h.freeze == nil {
		Err(c, apierr.New(apierr.CodeInternal, "冻结确认服务未初始化"))
		return
	}
	res, err := h.freeze.Confirm(c.Request.Context(), &in)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, res)
}
