package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ledger/internal/config"
	"ledger/internal/pkg/apierr"
	"ledger/internal/service"
	"ledger/internal/store"
)

// ocrResult 与真实 OCR 服务 data 字段一致的样例。
var ocrResult = map[string]any{
	"id": "x", "source": "pc_local",
	"transaction_time": "2026-11-10 12:00:00",
	"transaction_type": "expense",
	"amount_cents":     3500,
	"amount":           35.0,
	"account":          "微信零钱",
	"category":         "餐饮",
	"merchant":         "淘宝",
	"remark":           "午餐",
	"confidence":       map[string]any{"amount": 0.3},
	"need_review":      true,
	"raw_text":         "mock",
}

// newOCREnv 起假 OCR 服务（按真实 Python 契约响应）并装配 OCRService。
func newOCREnv(t *testing.T, fn func(w http.ResponseWriter, r *http.Request, body map[string]any)) (
	*service.OCRService, *testEnv) {
	t.Helper()
	env := newTestEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		fn(w, r, body)
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{OCREndpoint: srv.URL, DataDir: env.dir, Version: "1.0.0"}
	ssStore := service.NewScreenshotStore(env.dir)
	ssRepo := store.NewScreenshotRepo(env.s)
	ocr := service.NewOCRService(cfg, env.s, ssStore, ssRepo, env.mast, env.bal)
	return ocr, env
}

func jsonResp(w http.ResponseWriter, code int, obj any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(obj)
}

// TestOCRHintOmittedAndDataUnwrapped 空 hint 不出现在请求体；success/data 能解出结果。
func TestOCRHintOmittedAndDataUnwrapped(t *testing.T) {
	var gotBody map[string]any
	ocr, _ := newOCREnv(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if r.URL.Path != "/api/v1/recognize" {
			jsonResp(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "NOT_FOUND"}})
			return
		}
		gotBody = body
		jsonResp(w, http.StatusOK, map[string]any{
			"success": true, "data": ocrResult, "model_used": "mock",
			"elapsed_ms": 17, "task_id": nil, "status": "done",
		})
	})

	res, err := ocr.Recognize(context.Background(), &service.RecognizeInput{
		File: []byte("fake-jpeg-bytes"), Ext: ".jpg", Mode: "sync",
	})
	if err != nil {
		t.Fatalf("sync 识别失败: %v", err)
	}
	if _, has := gotBody["hint"]; has {
		t.Fatalf("空 hint 不应出现在请求体: %v", gotBody)
	}
	if gotBody["mode"] != "sync" || gotBody["model"] != "auto" {
		t.Fatalf("请求体字段错误: %v", gotBody)
	}
	if res.Status != "done" || res.Result == nil {
		t.Fatalf("响应解析错误: %+v", res)
	}
	// data 解包 + Go 侧名称回填
	if res.Result["category_id"] != "cat_e01" {
		t.Fatalf("category_id 回填错误: %v", res.Result["category_id"])
	}
	if res.Result["account_id"] != "acc_wx00001" {
		t.Fatalf("account_id 回填错误: %v", res.Result["account_id"])
	}
	if res.Result["transaction_time"] != "2026-11-10 12:00:00" {
		t.Fatalf("transaction_time 透传错误: %v", res.Result["transaction_time"])
	}
	// hint 归一化：wechat → wechat
	ocr2, _ := newOCREnv(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if body["hint"] != "wechat" {
			t.Errorf("hint 归一化失败: %v", body["hint"])
		}
		jsonResp(w, http.StatusOK, map[string]any{"success": true, "data": ocrResult,
			"model_used": "mock", "elapsed_ms": 1, "status": "done"})
	})
	if _, err = ocr2.Recognize(context.Background(), &service.RecognizeInput{
		File: []byte("x"), Ext: ".jpg", Mode: "sync", Hint: "微信"}); err != nil {
		t.Fatalf("带 hint 识别失败: %v", err)
	}
}

// TestOCRUpstream4xxIsError 4xx（真实错误体 {error:{code,message}}）必须返回错误而不是假成功。
func TestOCRUpstream4xxIsError(t *testing.T) {
	ocr, env := newOCREnv(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		jsonResp(w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"code": "INVALID_IMAGE", "message": "图片打开失败：Truncated File Read",
			"details": map[string]any{"path": "/app/data/x.jpg"},
		}})
	})
	_, err := ocr.Recognize(context.Background(), &service.RecognizeInput{
		File: []byte("bad"), Ext: ".jpg", Mode: "sync",
	})
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeOCRUpstream {
		t.Fatalf("4xx 应返回 OCR_UPSTREAM_ERROR, got %v", err)
	}
	if apierr.StatusOf(ae) != http.StatusBadGateway {
		t.Fatalf("OCR_UPSTREAM_ERROR 应为 502, got %d", apierr.StatusOf(ae))
	}
	if ae.Details["upstream_code"] != "INVALID_IMAGE" || ae.Details["status"] != http.StatusBadRequest {
		t.Fatalf("错误 details 未透传上游信息: %+v", ae.Details)
	}
	// 截图状态应为 failed，不能留 done
	repo := store.NewScreenshotRepo(env.s)
	var n int
	if err := env.s.SQL.QueryRow(`SELECT COUNT(1) FROM screenshots WHERE ocr_status='done'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("失败请求不应写 done, done rows=%d", n)
	}
	_ = repo
}

// TestOCRMissingKeysIsError 包装体 success=true 但缺关键字段 → 失败（不得假成功）。
func TestOCRMissingKeysIsError(t *testing.T) {
	ocr, _ := newOCREnv(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		jsonResp(w, http.StatusOK, map[string]any{"success": true,
			"data": map[string]any{"warnings": []string{"金额未识别"}}})
	})
	_, err := ocr.Recognize(context.Background(), &service.RecognizeInput{
		File: []byte("x"), Ext: ".jpg", Mode: "sync",
	})
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeOCRUpstream {
		t.Fatalf("缺关键字段应返回 OCR_UPSTREAM_ERROR, got %v", err)
	}
}

// TestOCRCodeNonZeroIsError 兼容 {code:非0,...} 包装。
func TestOCRCodeNonZeroIsError(t *testing.T) {
	ocr, _ := newOCREnv(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		jsonResp(w, http.StatusOK, map[string]any{"code": 1001,
			"error": map[string]any{"code": "BAD_INPUT", "message": "hint 非法"}})
	})
	_, err := ocr.Recognize(context.Background(), &service.RecognizeInput{
		File: []byte("x"), Ext: ".jpg", Mode: "sync",
	})
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeOCRUpstream {
		t.Fatalf("code!=0 应返回 OCR_UPSTREAM_ERROR, got %v", err)
	}
}

// TestOCRAsyncMissingTaskID 异步响应缺 task_id → 502（不是 500）。
func TestOCRAsyncMissingTaskID(t *testing.T) {
	ocr, _ := newOCREnv(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		jsonResp(w, http.StatusAccepted, map[string]any{"success": true, "status": "pending"})
	})
	_, err := ocr.Recognize(context.Background(), &service.RecognizeInput{
		File: []byte("x"), Ext: ".jpg", Mode: "async",
	})
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeOCRUpstream {
		t.Fatalf("缺 task_id 应返回 OCR_UPSTREAM_ERROR, got %v", err)
	}
	if apierr.StatusOf(ae) != http.StatusBadGateway {
		t.Fatalf("应为 502, got %d", apierr.StatusOf(ae))
	}
}

// TestOCRTaskFailedIsError 任务 status=failed → 错误（不返回 done）。
func TestOCRTaskFailedIsError(t *testing.T) {
	ocr, _ := newOCREnv(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch r.URL.Path {
		case "/api/v1/recognize":
			jsonResp(w, http.StatusAccepted, map[string]any{"success": true, "task_id": "task_x", "status": "pending"})
		case "/api/v1/tasks/task_x":
			jsonResp(w, http.StatusOK, map[string]any{"task_id": "task_x", "status": "failed",
				"progress": 0, "result": nil, "error": "模型推理超时"})
		default:
			jsonResp(w, http.StatusNotFound, map[string]any{})
		}
	})
	res, err := ocr.Recognize(context.Background(), &service.RecognizeInput{
		File: []byte("x"), Ext: ".jpg", Mode: "async",
	})
	if err != nil {
		t.Fatalf("async 创建失败: %v", err)
	}
	if res.TaskID != "task_x" {
		t.Fatalf("task_id 解析错误: %q", res.TaskID)
	}
	_, err = ocr.Task(context.Background(), "task_x")
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeOCRUpstream {
		t.Fatalf("failed 任务应返回错误, got %v", err)
	}
	if ae.Details["status"] != "failed" {
		t.Fatalf("错误 details 应含 status=failed: %+v", ae.Details)
	}
}

// TestOCRTaskDoneUnwrap 任务 done 时按 {result:{...}} 解包并回填。
func TestOCRTaskDoneUnwrap(t *testing.T) {
	ocr, _ := newOCREnv(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch r.URL.Path {
		case "/api/v1/recognize":
			jsonResp(w, http.StatusAccepted, map[string]any{"success": true, "task_id": "task_ok", "status": "pending"})
		case "/api/v1/tasks/task_ok":
			jsonResp(w, http.StatusOK, map[string]any{"task_id": "task_ok", "status": "done",
				"progress": 100, "result": ocrResult, "error": nil,
				"elapsed_ms": 88, "model_used": "mock"})
		default:
			jsonResp(w, http.StatusNotFound, map[string]any{})
		}
	})
	if _, err := ocr.Recognize(context.Background(), &service.RecognizeInput{
		File: []byte("x"), Ext: ".jpg", Mode: "async",
	}); err != nil {
		t.Fatalf("async 创建失败: %v", err)
	}
	body, err := ocr.Task(context.Background(), "task_ok")
	if err != nil {
		t.Fatalf("任务查询失败: %v", err)
	}
	if body["status"] != "done" {
		t.Fatalf("status = %v", body["status"])
	}
	result, _ := body["result"].(map[string]any)
	if result == nil || result["category_id"] != "cat_e01" {
		t.Fatalf("任务结果未回填: %+v", body)
	}
	if body["ocr_result_path"] == nil {
		t.Fatal("ocr_result_path 缺失")
	}
}

// TestOCRUpstream5xxIsUpstreamError 上游 5xx → 502 OCR_UPSTREAM_ERROR（区分于网络不可达的 503）。
func TestOCRUpstream5xxIsUpstreamError(t *testing.T) {
	ocr, _ := newOCREnv(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		jsonResp(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{
			"code": "MODEL_ERROR", "message": "模型推理异常"}})
	})
	_, err := ocr.Recognize(context.Background(), &service.RecognizeInput{
		File: []byte("x"), Ext: ".jpg", Mode: "sync",
	})
	ae, ok := err.(*apierr.Error)
	if !ok || ae.Code != apierr.CodeOCRUpstream {
		t.Fatalf("上游 5xx 应返回 OCR_UPSTREAM_ERROR, got %v", err)
	}
	if apierr.StatusOf(ae) != http.StatusBadGateway {
		t.Fatalf("应为 502, got %d", apierr.StatusOf(ae))
	}
	if ae.Details["upstream_code"] != "MODEL_ERROR" {
		t.Fatalf("upstream_code 未透传: %+v", ae.Details)
	}
}
