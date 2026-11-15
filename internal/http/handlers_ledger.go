package http

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/ids"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// LedgerHandler 账本处理器（含规则全量替换与视图重建）。
type LedgerHandler struct {
	s      *store.Store
	master *store.MasterRepo
	views  *store.ViewManager
}

// NewLedgerHandler 构造。
func NewLedgerHandler(s *store.Store, master *store.MasterRepo, views *store.ViewManager) *LedgerHandler {
	return &LedgerHandler{s: s, master: master, views: views}
}

func (h *LedgerHandler) ledgerTransactionCount(ledgerID string) (*int64, error) {
	where, args, err := h.views.BuildLedgerWhere(ledgerID)
	if err != nil {
		return nil, err
	}
	n, err := h.master.CountLedgerTransactions(where, args)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (h *LedgerHandler) toDTO(l *store.Ledger, withStats bool) (LedgerDTO, error) {
	dto := LedgerDTO{
		ID: l.ID, Name: l.Name, Currency: l.Currency, Remark: l.Remark,
		IsDefault: l.IsDefault, SortOrder: l.SortOrder, IsArchived: l.IsArchived,
		Rules:     []LedgerRuleDTO{},
		CreatedAt: timeutil.FormatLocal(l.CreatedAt), UpdatedAt: timeutil.FormatLocal(l.UpdatedAt),
	}
	rules, targets, err := h.master.LedgerRules(l.ID)
	if err != nil {
		return dto, err
	}
	for _, r := range rules {
		rd := LedgerRuleDTO{ID: r.ID, MatchMode: r.MatchMode, Targets: []LedgerRuleTargetDTO{}}
		for _, t := range targets[r.ID] {
			rd.Targets = append(rd.Targets, LedgerRuleTargetDTO{TargetType: t.TargetType, TargetID: t.TargetID})
		}
		dto.Rules = append(dto.Rules, rd)
	}
	if withStats {
		cnt, err := h.ledgerTransactionCount(l.ID)
		if err != nil {
			return dto, err
		}
		dto.TransactionCount = cnt
	}
	return dto, nil
}

// List GET /api/ledgers?include_archived=&with_stats=
func (h *LedgerHandler) List(c *gin.Context) {
	ledgers, err := h.master.ListLedgers(c.Query("include_archived") == "true")
	if err != nil {
		Err(c, err)
		return
	}
	withStats := c.Query("with_stats") == "true"
	items := make([]LedgerDTO, 0, len(ledgers))
	for i := range ledgers {
		dto, err := h.toDTO(&ledgers[i], withStats)
		if err != nil {
			Err(c, err)
			return
		}
		items = append(items, dto)
	}
	OK(c, gin.H{"total": len(items), "page": 1, "page_size": len(items), "items": items})
}

type ledgerRuleBody struct {
	MatchMode string `json:"match_mode"`
	Targets   []struct {
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
	} `json:"targets"`
}

type ledgerUpsertBody struct {
	Name      string           `json:"name"`
	Currency  string           `json:"currency"`
	Remark    string           `json:"remark"`
	IsDefault *int             `json:"is_default"`
	SortOrder *int             `json:"sort_order"`
	Rules     []ledgerRuleBody `json:"rules"`
}

var validTargetTypes = map[string]bool{
	"category_l1": true, "category_l2": true, "tag": true, "account": true, "merchant": true,
}

func validateRules(c *gin.Context, rules []ledgerRuleBody) bool {
	for _, r := range rules {
		if r.MatchMode != "" && r.MatchMode != "or" && r.MatchMode != "all" {
			BadRequest(c, "match_mode 必须为 or|all")
			return false
		}
		for _, t := range r.Targets {
			if !validTargetTypes[t.TargetType] || t.TargetID == "" {
				BadRequest(c, "target_type/target_id 非法")
				return false
			}
		}
	}
	return true
}

// saveRules 规则落库（body → 模型）并重建视图。
func (h *LedgerHandler) saveRules(c *gin.Context, ledgerID string, bodies []ledgerRuleBody) bool {
	now := timeutil.NowUnix()
	rules := make([]store.LedgerRule, 0, len(bodies))
	targets := map[string][]store.LedgerRuleTarget{}
	for _, rb := range bodies {
		rule := store.LedgerRule{
			ID: ids.ShortID("rule_"), LedgerID: ledgerID,
			MatchMode: nnStr(rb.MatchMode, "or"), IsActive: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		for _, t := range rb.Targets {
			targets[rule.ID] = append(targets[rule.ID], store.LedgerRuleTarget{
				RuleID: rule.ID, TargetType: t.TargetType, TargetID: t.TargetID,
			})
		}
		rules = append(rules, rule)
	}
	if err := h.master.ReplaceLedgerRules(ledgerID, rules, targets); err != nil {
		Err(c, err)
		return false
	}
	if err := h.views.RebuildLedgerView(ledgerID); err != nil {
		Err(c, err)
		return false
	}
	return true
}

// Create POST /api/ledgers。
func (h *LedgerHandler) Create(c *gin.Context) {
	var b ledgerUpsertBody
	if err := c.ShouldBindJSON(&b); err != nil || b.Name == "" {
		BadRequest(c, "name 必填")
		return
	}
	if !validateRules(c, b.Rules) {
		return
	}
	now := timeutil.NowUnix()
	l := store.Ledger{ID: ids.ShortID("ldg_"), Name: b.Name, Currency: nnStr(b.Currency, "CNY"),
		Remark: b.Remark, CreatedAt: now, UpdatedAt: now}
	if b.IsDefault != nil {
		l.IsDefault = *b.IsDefault
	}
	if b.SortOrder != nil {
		l.SortOrder = *b.SortOrder
	}
	if l.IsDefault == 1 {
		h.s.G.Model(&store.Ledger{}).Where("is_default = 1").Update("is_default", 0)
	}
	if err := h.s.G.Create(&l).Error; err != nil {
		if isUniqueViolation(err) {
			ErrCode(c, apierr.CodeDuplicate, "已存在同名账本", nil)
			return
		}
		Err(c, err)
		return
	}
	if len(b.Rules) > 0 {
		if !h.saveRules(c, l.ID, b.Rules) {
			return
		}
	} else if err := h.views.RebuildLedgerView(l.ID); err != nil {
		Err(c, err)
		return
	}
	dto, err := h.toDTO(&l, true)
	if err != nil {
		Err(c, err)
		return
	}
	Created(c, dto)
}

// Get GET /api/ledgers/:id（含规则与交易数）。
func (h *LedgerHandler) Get(c *gin.Context) {
	l, err := h.master.GetLedger(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "账本不存在")
			return
		}
		Err(c, err)
		return
	}
	dto, err := h.toDTO(l, true)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, dto)
}

// Update PUT /api/ledgers/:id（更新基本信息并重建视图）。
func (h *LedgerHandler) Update(c *gin.Context) {
	l, err := h.master.GetLedger(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "账本不存在")
			return
		}
		Err(c, err)
		return
	}
	var b ledgerUpsertBody
	if err := c.ShouldBindJSON(&b); err != nil {
		BadRequest(c, "请求体非法")
		return
	}
	if b.Name != "" {
		l.Name = b.Name
	}
	if b.Currency != "" {
		l.Currency = b.Currency
	}
	l.Remark = b.Remark
	if b.IsDefault != nil {
		l.IsDefault = *b.IsDefault
	}
	if b.SortOrder != nil {
		l.SortOrder = *b.SortOrder
	}
	if l.IsDefault == 1 {
		h.s.G.Model(&store.Ledger{}).Where("is_default = 1 AND id <> ?", l.ID).Update("is_default", 0)
	}
	if err := h.s.G.Save(l).Error; err != nil {
		if isUniqueViolation(err) {
			ErrCode(c, apierr.CodeDuplicate, "已存在同名账本", nil)
			return
		}
		Err(c, err)
		return
	}
	if b.Rules != nil {
		if !validateRules(c, b.Rules) {
			return
		}
		if !h.saveRules(c, l.ID, b.Rules) {
			return
		}
	}
	dto, err := h.toDTO(l, true)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, dto)
}

// Archive POST /api/ledgers/:id/archive {archived}。
func (h *LedgerHandler) Archive(c *gin.Context) {
	l, err := h.master.GetLedger(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "账本不存在")
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
		l.IsArchived = 1
		now := timeutil.NowUnix()
		l.ArchivedAt = &now
	} else {
		l.IsArchived = 0
		l.ArchivedAt = nil
	}
	if err := h.s.G.Save(l).Error; err != nil {
		Err(c, err)
		return
	}
	dto, err := h.toDTO(l, false)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, dto)
}

// Delete DELETE /api/ledgers/:id（删除 + DROP VIEW）。
func (h *LedgerHandler) Delete(c *gin.Context) {
	l, err := h.master.GetLedger(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "账本不存在")
			return
		}
		Err(c, err)
		return
	}
	if l.IsDefault == 1 {
		ErrCode(c, apierr.CodeValidation, "默认账本不可删除", nil)
		return
	}
	if err := h.s.G.Delete(&store.Ledger{}, "id = ?", l.ID).Error; err != nil {
		Err(c, err)
		return
	}
	if err := h.views.DropLedgerView(l.ID); err != nil {
		Err(c, err)
		return
	}
	OK(c, gin.H{"ok": true})
}

// GetRules GET /api/ledgers/:id/rules。
func (h *LedgerHandler) GetRules(c *gin.Context) {
	if _, err := h.master.GetLedger(c.Param("id")); err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "账本不存在")
			return
		}
		Err(c, err)
		return
	}
	rules, targets, err := h.master.LedgerRules(c.Param("id"))
	if err != nil {
		Err(c, err)
		return
	}
	items := []LedgerRuleDTO{}
	for _, r := range rules {
		rd := LedgerRuleDTO{ID: r.ID, MatchMode: r.MatchMode, Targets: []LedgerRuleTargetDTO{}}
		for _, t := range targets[r.ID] {
			rd.Targets = append(rd.Targets, LedgerRuleTargetDTO{TargetType: t.TargetType, TargetID: t.TargetID})
		}
		items = append(items, rd)
	}
	OK(c, gin.H{"items": items})
}

// PutRules PUT /api/ledgers/:id/rules（全量替换 + 重建视图）。
func (h *LedgerHandler) PutRules(c *gin.Context) {
	l, err := h.master.GetLedger(c.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			NotFound(c, "账本不存在")
			return
		}
		Err(c, err)
		return
	}
	var b struct {
		Rules []ledgerRuleBody `json:"rules"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		BadRequest(c, "请求体非法")
		return
	}
	if !validateRules(c, b.Rules) {
		return
	}
	if !h.saveRules(c, l.ID, b.Rules) {
		return
	}
	dto, err := h.toDTO(l, true)
	if err != nil {
		Err(c, err)
		return
	}
	OK(c, dto)
}
