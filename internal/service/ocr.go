package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ledger/internal/config"
	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// OCRService OCR 代理：转发 Python 服务、任务状态代理、结果后处理（§5.8）。
type OCRService struct {
	cfg     *config.Config
	s       *store.Store
	ss      *ScreenshotStore
	repo    *store.ScreenshotRepo
	master  *store.MasterRepo
	balance *BalanceService
	client  *http.Client
	mu      sync.Mutex
	tasks   map[string]*ocrTaskLink // task_id → 截图关联（内存，TTL 1h）
}

type ocrTaskLink struct {
	screenshotID string
	createdAt    time.Time
	processed    bool
}

// NewOCRService 构造。
func NewOCRService(cfg *config.Config, s *store.Store, ss *ScreenshotStore,
	repo *store.ScreenshotRepo, master *store.MasterRepo, balance *BalanceService) *OCRService {
	return &OCRService{
		cfg: cfg, s: s, ss: ss, repo: repo, master: master, balance: balance,
		client: &http.Client{Timeout: 120 * time.Second},
		tasks:  map[string]*ocrTaskLink{},
	}
}

// Available 探测 Python 服务（500ms 超时，不阻塞）。
func (o *OCRService) Available() bool {
	cli := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := cli.Get(o.cfg.OCREndpoint + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (o *OCRService) unavailable() *apierr.Error {
	return apierr.New(apierr.CodeOCRUnavailable, "OCR 服务未启动，可手动录入或稍后重试").
		WithDetails(map[string]any{"endpoint": o.cfg.OCREndpoint})
}

// doJSON POST/GET JSON。
func (o *OCRService) doJSON(method, path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, o.cfg.OCREndpoint+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, o.unavailable()
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &out); err != nil {
			return nil, apierr.Newf(apierr.CodeOCRUpstream, "OCR 服务响应非 JSON: %s", truncate(string(raw), 200))
		}
	}
	if out == nil {
		out = map[string]any{}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode >= 500 {
			// 服务在跑但内部错误 → 上游错误（502）；网络不可达才是 OCR_UNAVAILABLE(503)
			code, msg := upstreamErr(out, raw)
			return nil, apierr.Newf(apierr.CodeOCRUpstream, "OCR 服务内部错误(%d): %s", resp.StatusCode, msg).
				WithDetails(map[string]any{
					"endpoint":      o.cfg.OCREndpoint,
					"status":        resp.StatusCode,
					"upstream_code": code,
				})
		}
		// 4xx 是 OCR 服务对本请求的拒绝（hint 非法 / 图片不可读等），一律按失败处理
		code, msg := upstreamErr(out, raw)
		return nil, apierr.Newf(apierr.CodeOCRUpstream, "OCR 服务拒绝请求(%d): %s", resp.StatusCode, msg).
			WithDetails(map[string]any{
				"endpoint":      o.cfg.OCREndpoint,
				"status":        resp.StatusCode,
				"upstream_code": code,
			})
	}
	return out, nil
}

// normalizeHint 把外部 hint 归一化为 OCR 服务接受的三个字面量之一；无法识别返回空（省略字段）。
func normalizeHint(h string) string {
	switch strings.ToLower(strings.TrimSpace(h)) {
	case "wechat", "微信":
		return "wechat"
	case "alipay", "支付宝":
		return "alipay"
	case "receipt", "收据", "小票", "发票":
		return "receipt"
	default:
		return ""
	}
}

// truncate 截断日志/错误信息长度。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// upstreamErr 解析 OCR 服务错误体：{"error":{"code","message","details"}}（主），
// 兼容 FastAPI 的 {"detail":...} 与纯文本。
func upstreamErr(body map[string]any, raw []byte) (code, message string) {
	if e, ok := body["error"]; ok {
		switch v := e.(type) {
		case map[string]any:
			code, _ = v["code"].(string)
			message, _ = v["message"].(string)
			if message == "" {
				if b, err := json.Marshal(v); err == nil {
					message = string(b)
				}
			}
			return code, truncate(message, 300)
		default:
			return "", truncate(fmt.Sprint(v), 300)
		}
	}
	if d, ok := body["detail"]; ok {
		if b, err := json.Marshal(d); err == nil {
			return "", truncate(string(b), 300)
		}
	}
	return "", truncate(string(raw), 300)
}

// RecognizeInput OCR 识别输入。
type RecognizeInput struct {
	File         []byte
	Ext          string
	ScreenshotID string
	Model        string
	Mode         string // sync|async
	Hint         string
}

// RecognizeResult /api/ocr/recognize 响应。
type RecognizeResult struct {
	TaskID         string         `json:"task_id,omitempty"`
	ScreenshotID   string         `json:"screenshot_id"`
	Status         string         `json:"status"`
	OriginalPath   string         `json:"original_path"`
	CompressedPath string         `json:"compressed_path"`
	ElapsedMs      int64          `json:"elapsed_ms,omitempty"`
	ModelUsed      string         `json:"model_used,omitempty"`
	Result         map[string]any `json:"result,omitempty"`
	OCRResultPath  string         `json:"ocr_result_path,omitempty"`
}

// Recognize 上传识别（sync 直接返回结果，async 返回 task_id 供轮询）。
func (o *OCRService) Recognize(ctx context.Context, in *RecognizeInput) (*RecognizeResult, error) {
	if !o.Available() {
		return nil, o.unavailable()
	}
	var ss *store.Screenshot
	var err error
	if in.ScreenshotID != "" {
		ss, err = o.repo.Get(in.ScreenshotID)
		if err != nil {
			return nil, err
		}
		if ss == nil {
			return nil, apierr.New(apierr.CodeNotFound, "截图不存在")
		}
	} else {
		if len(in.File) == 0 {
			return nil, apierr.New(apierr.CodeValidation, "file 或 screenshot_id 必填")
		}
		if ss, err = o.ss.Save(ctx, o.s, o.repo, in.File, in.Ext); err != nil {
			return nil, err
		}
	}
	if in.Mode == "" {
		in.Mode = "sync"
	}
	model := in.Model
	if model == "" || model == "auto" {
		model = "auto"
	}
	absPath := o.ss.Abs(ss.OriginalPath)

	_ = o.repo.UpdateOCR(ss.ID, "processing", "", "", 0, 0)
	started := time.Now()
	reqBody := map[string]any{
		"image_path": absPath,
		"model":      model,
		"mode":       in.Mode,
	}
	// Python 侧 hint 为 Literal['receipt','wechat','alipay']，空串/非法值会 422：
	// 已知值归一化后下发，未知值直接省略（服务端默认 receipt）
	if h := normalizeHint(in.Hint); h != "" {
		reqBody["hint"] = h
	}
	body, err := o.doJSON("POST", "/api/v1/recognize", reqBody)
	if err != nil {
		_ = o.repo.UpdateOCR(ss.ID, "failed", "", "", 0, 0)
		return nil, err
	}
	res := &RecognizeResult{
		ScreenshotID: ss.ID, OriginalPath: ss.OriginalPath, CompressedPath: ss.CompressedPath,
	}

	if in.Mode == "async" {
		taskID, _ := body["task_id"].(string)
		if taskID == "" {
			if data, ok := body["data"].(map[string]any); ok {
				taskID, _ = data["task_id"].(string)
			}
		}
		if taskID == "" {
			return nil, apierr.New(apierr.CodeOCRUpstream, "OCR 服务未返回 task_id").
				WithDetails(map[string]any{"endpoint": o.cfg.OCREndpoint})
		}
		o.mu.Lock()
		o.tasks[taskID] = &ocrTaskLink{screenshotID: ss.ID, createdAt: time.Now()}
		o.mu.Unlock()
		res.TaskID = taskID
		res.Status = "processing"
		return res, nil
	}

	// sync：{success,data:{...}}（PRD 7.4）
	rawResult, err := extractResult(body)
	if err != nil {
		_ = o.repo.UpdateOCR(ss.ID, "failed", "", "", 0, 0)
		return nil, err
	}
	processed, needReview := o.postProcess(rawResult)
	elapsed := time.Since(started).Milliseconds()
	if v, ok := body["elapsed_ms"].(float64); ok && v > 0 {
		elapsed = int64(v)
	}
	modelUsed, _ := body["model_used"].(string)
	if modelUsed == "" {
		modelUsed, _ = body["model"].(string)
	}
	resultPath, err := o.writeResultFile(ss.ID, processed)
	if err != nil {
		return nil, err
	}
	nr := 0
	if needReview {
		nr = 1
	}
	if err = o.repo.UpdateOCR(ss.ID, "done", modelUsed, resultPath, elapsed, nr); err != nil {
		return nil, err
	}
	res.Status = "done"
	res.ElapsedMs = elapsed
	res.ModelUsed = modelUsed
	res.Result = processed
	res.OCRResultPath = resultPath
	return res, nil
}

// extractResult 按真实契约解包并校验：
// 同步 {"success":true,"data":{...}} → 取 data；任务 {"result":{...}} → 取 result；否则根对象。
// code!=0、success=false、解包后缺 OCR 关键字段（transaction_time/amount_cents/amount）均视为失败，
// 避免把 OCR 的校验错误当成识别结果写库。
func extractResult(body map[string]any) (map[string]any, error) {
	if c, ok := body["code"]; ok && !isZeroCode(c) {
		code, msg := upstreamErr(body, nil)
		if code == "" {
			code = fmt.Sprint(c)
		}
		return nil, apierr.Newf(apierr.CodeOCRUpstream, "OCR 服务返回失败(code=%s): %s", code, msg)
	}
	if ok, has := body["success"].(bool); has && !ok {
		code, msg := upstreamErr(body, nil)
		return nil, apierr.Newf(apierr.CodeOCRUpstream, "OCR 识别失败: %s%s", code, msg)
	}
	candidate := body
	if d, ok := body["data"].(map[string]any); ok {
		candidate = d
	} else if r, ok := body["result"].(map[string]any); ok {
		candidate = r
	}
	if !hasOCRKey(candidate) {
		return nil, apierr.New(apierr.CodeOCRUpstream, "OCR 服务响应缺少关键字段（transaction_time/amount_cents/amount）").
			WithDetails(map[string]any{"body": truncate(fmt.Sprint(candidate), 300)})
	}
	return candidate, nil
}

// hasUpstreamError 判断任务响应是否带非空 error 字段。
func hasUpstreamError(body map[string]any) bool {
	v, ok := body["error"]
	if !ok || v == nil {
		return false
	}
	return strings.TrimSpace(fmt.Sprint(v)) != ""
}

// isZeroCode 判断 code 是否为 0（int/float64/json.Number/字符串 "0"）。
func isZeroCode(c any) bool {
	switch v := c.(type) {
	case float64:
		return v == 0
	case int:
		return v == 0
	case int64:
		return v == 0
	case string:
		return v == "0" || v == ""
	default:
		return false
	}
}

// hasOCRKey 结果至少含一个 OCR 关键字段。
func hasOCRKey(m map[string]any) bool {
	if v, ok := m["transaction_time"]; ok && fmt.Sprint(v) != "" {
		return true
	}
	if _, ok := m["amount_cents"]; ok {
		return true
	}
	if _, ok := m["amount"]; ok {
		return true
	}
	return false
}

// Task 轮询任务（done 时补后处理与落库）。
func (o *OCRService) Task(ctx context.Context, taskID string) (map[string]any, error) {
	body, err := o.doJSON("GET", "/api/v1/tasks/"+taskID, nil)
	if err != nil {
		return nil, err
	}
	status, _ := body["status"].(string)
	if status == "not_found" {
		return nil, apierr.New(apierr.CodeOCRTaskNotFound, "OCR 任务不存在或已过期")
	}
	// 任务失败：把上游 error 透传，绝不返回 done
	if status == "failed" || hasUpstreamError(body) {
		msg := fmt.Sprint(body["error"])
		o.mu.Lock()
		link := o.tasks[taskID]
		o.mu.Unlock()
		if link != nil {
			_ = o.repo.UpdateOCR(link.screenshotID, "failed", "", "", 0, 0)
			link.processed = true
		}
		return nil, apierr.New(apierr.CodeOCRUpstream, "OCR 任务失败: "+truncate(msg, 300)).
			WithDetails(map[string]any{"task_id": taskID, "status": status})
	}
	o.mu.Lock()
	link := o.tasks[taskID]
	o.mu.Unlock()
	if link == nil {
		return body, nil // 非本进程发起，原样转发
	}
	if status == "done" && !link.processed {
		raw, err := extractResult(body)
		if err != nil {
			_ = o.repo.UpdateOCR(link.screenshotID, "failed", "", "", 0, 0)
			link.processed = true
			return nil, err
		}
		processed, needReview := o.postProcess(raw)
		elapsed, _ := body["elapsed_ms"].(float64)
		modelUsed, _ := body["model_used"].(string)
		resultPath, err2 := o.writeResultFile(link.screenshotID, processed)
		err = err2
		if err != nil {
			return nil, err
		}
		nr := 0
		if needReview {
			nr = 1
		}
		if err = o.repo.UpdateOCR(link.screenshotID, "done", modelUsed, resultPath, int64(elapsed), nr); err != nil {
			return nil, err
		}
		body["result"] = processed
		body["ocr_result_path"] = resultPath
		body["screenshot_id"] = link.screenshotID
		link.processed = true
	} else if status == "failed" {
		_ = o.repo.UpdateOCR(link.screenshotID, "failed", "", "", 0, 0)
	}
	return body, nil
}

// writeResultFile 落盘 ocr-results/YYYY-MM-DD/<id>.json。
func (o *OCRService) writeResultFile(screenshotID string, result map[string]any) (string, error) {
	dir, err := o.ss.DatedDir("ocr-results", time.Now())
	if err != nil {
		return "", err
	}
	buf, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	name := screenshotID + ".json"
	if err = os.WriteFile(filepath.Join(dir, name), buf, 0o644); err != nil {
		return "", err
	}
	rel := filepath.ToSlash(filepath.Join("ocr-results",
		time.Now().In(timeutil.Local()).Format("2006-01-02"), name))
	return rel, nil
}

// postProcess Go 侧校验（PRD 7.6 中需要数据库的三条：账户/分类存在、冻结区提示）。
// 返回处理后结果 + need_review。
func (o *OCRService) postProcess(result map[string]any) (map[string]any, bool) {
	warnings := []string{}
	if arr, ok := result["warnings"].([]any); ok {
		for _, w := range arr {
			if s, ok := w.(string); ok {
				warnings = append(warnings, s)
			}
		}
	}
	needReview := false
	matchByName := func(kind string) string {
		name, _ := result[kind].(string)
		if name == "" {
			return ""
		}
		switch kind {
		case "category":
			cats, err := o.master.ListCategories("", false)
			if err != nil {
				return ""
			}
			for _, c := range cats {
				if c.Name == name {
					return c.ID
				}
			}
		case "account":
			accs, err := o.master.ListAccounts(true)
			if err != nil {
				return ""
			}
			for _, a := range accs {
				if a.Name == name || a.Alias == name {
					return a.ID
				}
			}
		case "merchant":
			mchs, err := o.master.ListMerchants("", "", 0)
			if err != nil {
				return ""
			}
			for _, m := range mchs {
				if m.Name == name {
					return m.ID
				}
			}
		}
		return ""
	}
	setID := func(kind, idKey string) {
		name, _ := result[kind].(string)
		if name == "" {
			result[idKey] = nil
			return
		}
		if id := matchByName(kind); id != "" {
			result[idKey] = id
		} else {
			result[idKey] = nil
			warnings = append(warnings, kind+" 未在库中找到："+name)
			needReview = true
		}
	}
	setID("category", "category_id")
	setID("account", "account_id")
	setID("merchant", "merchant_id")

	// 冻结区提示
	if ts, ok := result["transaction_time"].(string); ok && ts != "" {
		if unix, err := timeutil.ParseLocal(ts); err == nil {
			boundary := o.balance.FreezeBoundary(time.Now().Unix())
			if unix < boundary {
				warnings = append(warnings, "交易时间落在冻结区，入账后不可直接修改")
				needReview = true
			}
		}
	}
	if nr, ok := result["need_review"].(bool); ok && nr {
		needReview = true
	}
	result["warnings"] = warnings
	result["need_review"] = needReview
	return result, needReview
}

// Model GET /api/ocr/model 代理。
func (o *OCRService) Model(ctx context.Context) (map[string]any, error) {
	return o.doJSON("GET", "/api/v1/model", nil)
}

// SwitchModel PUT /api/ocr/model 代理（热切换）。
func (o *OCRService) SwitchModel(ctx context.Context, name string) (map[string]any, error) {
	return o.doJSON("POST", "/api/v1/model/switch", map[string]any{"name": name, "unload_first": true})
}
