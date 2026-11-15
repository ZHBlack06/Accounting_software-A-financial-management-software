package http_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/xuri/excelize/v2"

	"ledger/internal/app"
	"ledger/internal/config"
)

// newTestApp 拉起完整装配（临时数据目录），返回 httptest 服务端与令牌。
func newTestApp(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	a, srv, token := newTestAppFull(t)
	_ = a
	return srv, token
}

// newTestAppFull 同上，但额外返回 *app.App（需要直接访问 DB/服务时使用）。
func newTestAppFull(t *testing.T) (*app.App, *httptest.Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ledger-http-")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.Port = 0
	a, err := app.New(cfg)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("装配应用失败: %v", err)
	}
	t.Cleanup(func() {
		_ = a.Shutdown(nil)
		os.RemoveAll(dir)
	})
	token, err := a.Meta.Get("auth_token")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return a, srv, token
}

// doJSON 发送 JSON 请求。
func doJSON(t *testing.T, srv *httptest.Server, token, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rdr = bytes.NewReader(buf)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// uploadProbe 上传文件到 probe 端点。
func uploadProbe(t *testing.T, srv *httptest.Server, token, fileName string, content []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("files", fileName)
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/api/import/sheet/probe", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// buildXLSX 造一个两表工作簿（账目 + 统计）。
func buildXLSX(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", "账目")
	rows := [][]any{
		{"日期", "分类", "备注", "金额"},
		{"43831", "餐饮", "午饭", "12.00"},
		{"43831", "交通", "地铁", "4.00"},
	}
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("账目", cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.NewSheet("统计"); err != nil {
		t.Fatal(err)
	}
	pivot := [][]any{{"求和项:金额", "分类"}, {"月份", "餐饮", "总计"}, {"2020年01月", "12.00", "12.00"}}
	for i, row := range pivot {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow("统计", cell, &row); err != nil {
			t.Fatal(err)
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestHealthVersion 版本号 1.2.0。
func TestHealthVersion(t *testing.T) {
	srv, _ := newTestApp(t)
	st, body := doJSON(t, srv, "", "GET", "/api/health", nil)
	if st != 200 {
		t.Fatalf("health 状态 = %d", st)
	}
	if body["version"] != config.Version {
		t.Errorf("version = %v，want %s", body["version"], config.Version)
	}
}

// TestProbeRequiresToken 未带 token → 401。
func TestProbeRequiresToken(t *testing.T) {
	srv, _ := newTestApp(t)
	st, body := uploadProbe(t, srv, "", "账目.xlsx", buildXLSX(t))
	if st != http.StatusUnauthorized {
		t.Fatalf("未认证状态 = %d，body=%v", st, body)
	}
}

// TestProbeXLSRejected .xls → 422 UNSUPPORTED_SPREADSHEET。
func TestProbeXLSRejected(t *testing.T) {
	srv, token := newTestApp(t)
	xls := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, bytes.Repeat([]byte{0}, 64)...)
	st, body := uploadProbe(t, srv, token, "旧账.xls", xls)
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("状态 = %d，body=%v", st, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "UNSUPPORTED_SPREADSHEET" {
		t.Errorf("错误码 = %v", errObj["code"])
	}
}

// TestProbePreviewCommit 三端点串联 + settings 时间精度 + 缺映射 400。
func TestProbePreviewCommit(t *testing.T) {
	srv, token := newTestApp(t)
	data := buildXLSX(t)

	st, probe := uploadProbe(t, srv, token, "账目.xlsx", data)
	if st != 200 {
		t.Fatalf("probe 状态 = %d，body=%v", st, probe)
	}
	batchID, _ := probe["batch_id"].(string)
	if batchID == "" {
		t.Fatal("probe 未返回 batch_id")
	}
	files, _ := probe["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files 长度 = %d", len(files))
	}
	file := files[0].(map[string]any)
	fileID, _ := file["file_id"].(string)
	sheets, _ := file["sheets"].([]any)
	if len(sheets) != 2 {
		t.Fatalf("sheet 数 = %d，want 2", len(sheets))
	}

	// 取默认账户
	st, accs := doJSON(t, srv, token, "GET", "/api/accounts", nil)
	if st != 200 {
		t.Fatalf("accounts 状态 = %d", st)
	}
	items, _ := accs["items"].([]any)
	accountID := items[0].(map[string]any)["id"].(string)

	// 缺金额映射 → 400 IMPORT_SHEET_INVALID
	st, body := doJSON(t, srv, token, "POST", "/api/import/sheet/preview", map[string]any{
		"batch_id": batchID,
		"defaults": map[string]any{"account_id": accountID},
		"sheets": []any{map[string]any{
			"file_id": fileID, "sheet_index": 0, "header_row": 1, "skip": false,
			"mapping":        map[string]any{"date": "日期"},
			"direction_rule": map[string]any{"mode": "form_default", "default": "expense"},
		}},
	})
	if st != http.StatusBadRequest {
		t.Fatalf("缺金额映射状态 = %d，body=%v", st, body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["code"] != "IMPORT_SHEET_INVALID" {
		t.Errorf("错误码 = %v，want IMPORT_SHEET_INVALID", errObj["code"])
	}

	// 完整映射 → preview 200 + commit 200
	plan := map[string]any{
		"file_id": fileID, "sheet_index": 0, "header_row": 1, "skip": false,
		"mapping":        map[string]any{"date": "日期", "amount": "金额", "category": "分类", "remark": "备注"},
		"direction_rule": map[string]any{"mode": "form_default", "default": "expense"},
		"time_precision": "day",
	}
	st, prev := doJSON(t, srv, token, "POST", "/api/import/sheet/preview", map[string]any{
		"batch_id": batchID, "defaults": map[string]any{"account_id": accountID},
		"sheets": []any{plan},
	})
	if st != 200 {
		t.Fatalf("preview 状态 = %d，body=%v", st, prev)
	}
	summary := prev["summary"].(map[string]any)
	if summary["parsed_ok"].(float64) != 2 {
		t.Errorf("parsed_ok = %v，want 2", summary["parsed_ok"])
	}

	st, commit := doJSON(t, srv, token, "POST", "/api/import/sheet/commit", map[string]any{
		"batch_id":                  batchID,
		"sheets":                    []any{map[string]any{"file_id": fileID, "sheet_index": 0}},
		"row_keys":                  []any{},
		"defaults":                  map[string]any{"account_id": accountID},
		"create_missing_categories": true, "create_missing_tags": true,
		"skip_duplicates": true, "allow_frozen": true,
	})
	if st != 200 {
		t.Fatalf("commit 状态 = %d，body=%v", st, commit)
	}
	if commit["created"].(float64) != 2 {
		t.Errorf("created = %v，want 2", commit["created"])
	}
	// v1.2 §9.7：缺省 freeze_mode=defer → 这两行（2020 年）应计为待确认
	if commit["pending_frozen"].(float64) != 2 {
		t.Errorf("pending_frozen = %v，want 2", commit["pending_frozen"])
	}

	// v1.2 §9.8：freeze_pending 过滤（1|true 只看待确认；0|false 只看已确认）
	st, onlyPending := doJSON(t, srv, token, "GET", "/api/transactions?freeze_pending=1", nil)
	if st != 200 || onlyPending["total"].(float64) != 2 {
		t.Errorf("freeze_pending=1 → 状态 %d total=%v（want 200/2）", st, onlyPending["total"])
	}
	st, onlyConfirmed := doJSON(t, srv, token, "GET", "/api/transactions?freeze_pending=0", nil)
	if st != 200 || onlyConfirmed["total"].(float64) != 0 {
		t.Errorf("freeze_pending=0 → 状态 %d total=%v（want 200/0）", st, onlyConfirmed["total"])
	}
	st, both := doJSON(t, srv, token, "GET", "/api/transactions?freeze_pending=true", nil)
	if st != 200 || both["total"].(float64) != 2 {
		t.Errorf("freeze_pending=true → 状态 %d total=%v", st, both["total"])
	}
	if st, _ := doJSON(t, srv, token, "GET", "/api/transactions?freeze_pending=maybe", nil); st != 400 {
		t.Errorf("非法 freeze_pending 状态 = %d，want 400", st)
	}
	// 列表里这些行必须是 freeze_pending=true 且 is_frozen=false（可写）
	if items, ok := onlyPending["items"].([]any); ok && len(items) > 0 {
		m, _ := items[0].(map[string]any)
		if m["freeze_pending"] != true || m["is_frozen"] != false {
			t.Errorf("待确认行 DTO 状态异常: freeze_pending=%v is_frozen=%v",
				m["freeze_pending"], m["is_frozen"])
		}
	}

	// 二次提交 → 409 IMPORT_BATCH_COMMITTED
	st, dup := doJSON(t, srv, token, "POST", "/api/import/sheet/commit", map[string]any{
		"batch_id":                  batchID,
		"sheets":                    []any{map[string]any{"file_id": fileID, "sheet_index": 0}},
		"defaults":                  map[string]any{"account_id": accountID},
		"create_missing_categories": true, "skip_duplicates": true, "allow_frozen": true,
	})
	if st != http.StatusConflict {
		t.Fatalf("二次提交状态 = %d，body=%v", st, dup)
	}
	if e, _ := dup["error"].(map[string]any); e["code"] != "IMPORT_BATCH_COMMITTED" {
		t.Errorf("错误码 = %v", e["code"])
	}

	// settings：time_precision 读写 + 非法值 400
	st, settings := doJSON(t, srv, token, "GET", "/api/settings", nil)
	if st != 200 || settings["time_precision"] != "second" {
		t.Fatalf("settings = %d %v", st, settings)
	}
	st, settings = doJSON(t, srv, token, "PUT", "/api/settings", map[string]any{"time_precision": "day"})
	if st != 200 || settings["time_precision"] != "day" {
		t.Fatalf("PUT settings = %d %v", st, settings)
	}
	st, _ = doJSON(t, srv, token, "PUT", "/api/settings", map[string]any{"time_precision": "minute"})
	if st != http.StatusBadRequest {
		t.Errorf("非法 time_precision 状态 = %d，want 400", st)
	}

	// 交易 DTO 带 time_precision
	st, txs := doJSON(t, srv, token, "GET", "/api/transactions?page=1&page_size=1", nil)
	if st != 200 {
		t.Fatalf("transactions 状态 = %d", st)
	}
	items, _ = txs["items"].([]any)
	tx := items[0].(map[string]any)
	if tx["time_precision"] != "day" {
		t.Errorf("导入行 time_precision = %v，want day", tx["time_precision"])
	}
	if _, ok := tx["transaction_time"].(string); !ok {
		t.Error("transaction_time 应为字符串")
	}

	// 批次审计列表
	st, batches := doJSON(t, srv, token, "GET", "/api/import/batches?limit=5", nil)
	if st != 200 || batches["total"].(float64) < 1 {
		t.Fatalf("batches = %d %v", st, batches)
	}
}
