package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"ledger/internal/config"
	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/service"
	"ledger/internal/store"
)

// MetaHandler meta/auth/settings 处理器。
type MetaHandler struct {
	cfg     *config.Config
	s       *store.Store
	meta    *store.MetaRepo
	mat     *service.Materializer
	started time.Time
}

// NewMetaHandler 构造。
func NewMetaHandler(cfg *config.Config, s *store.Store, meta *store.MetaRepo, mat *service.Materializer) *MetaHandler {
	return &MetaHandler{cfg: cfg, s: s, meta: meta, mat: mat, started: time.Now()}
}

// ocrAvailable 探测 OCR 服务（500ms 超时，不阻塞）。
func (h *MetaHandler) ocrAvailable() bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(h.cfg.OCREndpoint + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Health GET /api/health（免认证）。
func (h *MetaHandler) Health(c *gin.Context) {
	dbStatus := "ok"
	if err := h.s.SQL.Ping(); err != nil {
		dbStatus = "error"
	}
	ocr := "unavailable"
	if h.ocrAvailable() {
		ocr = "available"
	}
	OK(c, gin.H{
		"status":   "ok",
		"version":  h.cfg.Version,
		"db":       dbStatus,
		"ocr":      ocr,
		"uptime_s": int64(time.Since(h.started).Seconds()),
		"now":      timeutil.FormatLocal(time.Now().Unix()),
	})
}

// Login POST /api/auth/login（免认证）。
func (h *MetaHandler) Login(c *gin.Context) {
	var body struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Token == "" {
		BadRequest(c, "token 必填")
		return
	}
	want, err := h.meta.Get("auth_token")
	if err != nil {
		Err(c, err)
		return
	}
	if body.Token != want {
		ErrCode(c, apierr.CodeBadToken, "访问令牌错误", nil)
		return
	}
	OK(c, gin.H{"ok": true, "version": h.cfg.Version})
}

// AuthCheck GET /api/auth/check。
func (h *MetaHandler) AuthCheck(c *gin.Context) {
	OK(c, gin.H{"ok": true, "expires_in": nil})
}

// timePrecision 读取时间精度（'second' | 'day'，非法/空 → 'second'）。
func (h *MetaHandler) timePrecision(all map[string]string) string {
	if all["time_precision"] == "day" {
		return "day"
	}
	return "second"
}

// metaInt 读 int 型 meta。
func (h *MetaHandler) metaInt(all map[string]string, key string, def int64) int64 {
	if v, ok := all[key]; ok && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func strOrNil(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// Meta GET /api/meta（§5.1）。
func (h *MetaHandler) Meta(c *gin.Context) {
	all, err := h.meta.All()
	if err != nil {
		Err(c, err)
		return
	}
	now := time.Now().Unix()
	freezeDays := h.metaInt(all, "freeze_days", 30)
	boundary := now - freezeDays*86400
	lastFrozen := h.metaInt(all, "last_frozen_at", 0)

	var uiPrefs map[string]any
	_ = json.Unmarshal([]byte(all["ui_prefs"]), &uiPrefs)
	if uiPrefs == nil {
		uiPrefs = map[string]any{}
	}
	var lastFrozenStr any
	if lastFrozen > 0 {
		lastFrozenStr = timeutil.FormatLocal(lastFrozen)
	}
	OK(c, gin.H{
		"version":                     h.cfg.Version,
		"schema_version":              h.metaInt(all, "schema_version", 1),
		"freeze_days":                 freezeDays,
		"freeze_boundary":             timeutil.FormatLocal(boundary),
		"last_frozen_at":              lastFrozenStr,
		"current_time":                timeutil.FormatLocal(now),
		"timezone":                    "Asia/Shanghai",
		"default_account_id":          strOrNil(all["default_account_id"]),
		"default_ledger_id":           strOrNil(all["default_ledger_id"]),
		"default_category_expense_id": strOrNil(all["default_category_expense_id"]),
		"default_category_income_id":  strOrNil(all["default_category_income_id"]),
		"default_merchant_id":         strOrNil(all["default_merchant_id"]),
		"default_txn_type":            all["default_txn_type"],
		"last_used": gin.H{
			"account_id":  strOrNil(all["last_used_account_id"]),
			"category_id": strOrNil(all["last_used_category_id"]),
			"merchant_id": strOrNil(all["last_used_merchant_id"]),
		},
		"high_amount_threshold_cents": h.metaInt(all, "high_amount_threshold_cents", 50000),
		"ui_prefs":                    uiPrefs,
		"needs_rematerialize":         all["frozen_write_flag"] == "1",
		"ocr_available":               h.ocrAvailable(),
		"time_precision":              h.timePrecision(all),
	})
}

// settingsResponse 组装 settings（meta 的可写子集）。
func (h *MetaHandler) settingsResponse(c *gin.Context) {
	all, err := h.meta.All()
	if err != nil {
		Err(c, err)
		return
	}
	var uiPrefs map[string]any
	_ = json.Unmarshal([]byte(all["ui_prefs"]), &uiPrefs)
	if uiPrefs == nil {
		uiPrefs = map[string]any{}
	}
	var templates []any
	_ = json.Unmarshal([]byte(all["quick_templates"]), &templates)
	if templates == nil {
		templates = []any{}
	}
	theme, _ := uiPrefs["theme"].(string)
	OK(c, gin.H{
		"freeze_days":                 h.metaInt(all, "freeze_days", 30),
		"default_account_id":          strOrNil(all["default_account_id"]),
		"default_ledger_id":           strOrNil(all["default_ledger_id"]),
		"default_category_expense_id": strOrNil(all["default_category_expense_id"]),
		"default_category_income_id":  strOrNil(all["default_category_income_id"]),
		"default_merchant_id":         strOrNil(all["default_merchant_id"]),
		"default_txn_type":            all["default_txn_type"],
		"high_amount_threshold_cents": h.metaInt(all, "high_amount_threshold_cents", 50000),
		"quick_templates":             templates,
		"ui_prefs":                    uiPrefs,
		"theme":                       theme,
		"time_precision":              h.timePrecision(all),
	})
}

// GetSettings GET /api/settings。
func (h *MetaHandler) GetSettings(c *gin.Context) {
	h.settingsResponse(c)
}

// PutSettings PUT /api/settings（局部更新，返回完整 settings）。
// freeze_days 修改后立即触发一次物化重算（§5.1）。
func (h *MetaHandler) PutSettings(c *gin.Context) {
	var body map[string]json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		BadRequest(c, "请求体必须为 JSON 对象")
		return
	}
	freezeChanged := false
	setStr := func(key string) error {
		raw, ok := body[key]
		if !ok {
			return nil
		}
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if v == nil {
			return h.meta.Set(key, "")
		}
		return h.meta.Set(key, *v)
	}
	for _, k := range []string{"default_account_id", "default_ledger_id",
		"default_category_expense_id", "default_category_income_id", "default_merchant_id",
		"default_txn_type", "last_used_account_id", "last_used_category_id", "last_used_merchant_id"} {
		if err := setStr(k); err != nil {
			BadRequest(c, k+" 必须为字符串")
			return
		}
	}
	if raw, ok := body["freeze_days"]; ok {
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil || n < 10 || n > 365 {
			BadRequest(c, "freeze_days 必须为 10–365 的整数")
			return
		}
		if err := h.meta.Set("freeze_days", strconv.FormatInt(n, 10)); err != nil {
			Err(c, err)
			return
		}
		freezeChanged = true
	}
	if raw, ok := body["time_precision"]; ok {
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil || v == nil {
			BadRequest(c, "time_precision 必须为 second|day")
			return
		}
		if *v != "second" && *v != "day" {
			BadRequest(c, "time_precision 必须为 second|day")
			return
		}
		if err := h.meta.Set("time_precision", *v); err != nil {
			Err(c, err)
			return
		}
	}
	if raw, ok := body["high_amount_threshold_cents"]; ok {
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil || n < 0 {
			BadRequest(c, "high_amount_threshold_cents 必须为非负整数")
			return
		}
		if err := h.meta.Set("high_amount_threshold_cents", strconv.FormatInt(n, 10)); err != nil {
			Err(c, err)
			return
		}
	}
	if raw, ok := body["quick_templates"]; ok {
		// 原样存储 JSON 数组
		var arr []any
		if err := json.Unmarshal(raw, &arr); err != nil {
			BadRequest(c, "quick_templates 必须为数组")
			return
		}
		if err := h.meta.Set("quick_templates", string(raw)); err != nil {
			Err(c, err)
			return
		}
	}
	// ui_prefs：theme 与 ui_prefs 合并写入
	uiRaw, hasUI := body["ui_prefs"]
	themeRaw, hasTheme := body["theme"]
	if hasUI || hasTheme {
		all, err := h.meta.All()
		if err != nil {
			Err(c, err)
			return
		}
		var prefs map[string]any
		_ = json.Unmarshal([]byte(all["ui_prefs"]), &prefs)
		if prefs == nil {
			prefs = map[string]any{}
		}
		if hasUI {
			var patch map[string]any
			if err := json.Unmarshal(uiRaw, &patch); err != nil {
				BadRequest(c, "ui_prefs 必须为对象")
				return
			}
			for k, v := range patch {
				prefs[k] = v
			}
		}
		if hasTheme {
			var theme string
			if err := json.Unmarshal(themeRaw, &theme); err != nil || (theme != "light" && theme != "dark") {
				BadRequest(c, "theme 必须为 light|dark")
				return
			}
			prefs["theme"] = theme
		}
		buf, _ := json.Marshal(prefs)
		if err := h.meta.Set("ui_prefs", string(buf)); err != nil {
			Err(c, err)
			return
		}
	}
	if freezeChanged {
		// 立即触发一次物化重算（失败不阻断设置保存）
		_, _ = h.mat.Materialize(c.Request.Context(), time.Now())
	}
	h.settingsResponse(c)
}
