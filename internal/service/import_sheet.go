package service

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/ids"
	"ledger/internal/pkg/sheetparse"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// ImportSheetService 通用表格导入（Excel/CSV）三阶段编排：
// Probe（上传探测）→ Preview（映射解析预览）→ Commit（幂等分块入库）。
//
// 落盘布局（随 data/tmp 24h 自动清理，§1.2）：
//
//	data/tmp/import/<batchID>/manifest.json    批次清单（含每表映射计划与统计）
//	data/tmp/import/<batchID>/files/<fileID>   原始上传文件
//	data/tmp/import/<batchID>/rows-<n>.jsonl   预览行（提交时对比时间分布是否漂移）
type ImportSheetService struct {
	s       *store.Store
	meta    *store.MetaRepo
	master  *store.MasterRepo
	txn     *TxnService
	balance *BalanceService
	batches *store.ImportBatchRepo
	mat     *Materializer
	dataDir string
}

// NewImportSheetService 构造。mat 用于「导入即重算」（§9.4：不允许把错误余额留给下次查询）。
func NewImportSheetService(s *store.Store, meta *store.MetaRepo, master *store.MasterRepo,
	txn *TxnService, balance *BalanceService, batches *store.ImportBatchRepo,
	mat *Materializer, dataDir string) *ImportSheetService {
	return &ImportSheetService{s: s, meta: meta, master: master, txn: txn,
		balance: balance, batches: batches, mat: mat, dataDir: dataDir}
}

// 导入相关常量（§1.3 / §4.8）。
const (
	// importPreviewRows 预览响应返回的行数上限（全量落盘）。
	importPreviewRows = 200
	// importCommitChunk 每 200 行一个写事务（§4.8）。
	importCommitChunk = 200
	// importBatchTTL 批次有效期 24h。
	importBatchTTL = 24 * time.Hour
	// importTXIDLimit 超过该数量不返回 transaction_ids（避免响应膨胀）。
	importTXIDLimit = 500
	// importSampleTimes 时间分布样例的最大日期数。
	importSampleTimes = 3
)

// ---------------- 落盘清单 ----------------

// Manifest 批次清单（manifest.json）。
type Manifest struct {
	BatchID   string         `json:"batch_id"`
	CreatedAt int64          `json:"created_at"`
	Files     []ManifestFile `json:"files"`
	Warnings  []string       `json:"warnings"`
}

// ManifestFile 单文件清单。
type ManifestFile struct {
	FileID    string          `json:"file_id"`
	Index     int             `json:"index"`
	FileName  string          `json:"file_name"`
	Size      int64           `json:"file_size"`
	SHA1      string          `json:"sha1"`
	Kind      string          `json:"kind"`
	Encoding  string          `json:"encoding"`
	Date1904  bool            `json:"date_1904"`
	StoreName string          `json:"store_name"`
	Sheets    []ManifestSheet `json:"sheets"`
}

// ManifestSheet 单工作表清单（Plan 与 Stats 在 preview 后写入，commit 复用）。
type ManifestSheet struct {
	SheetIndex    int                   `json:"sheet_index"`
	SheetName     string                `json:"sheet_name"`
	RowCount      int                   `json:"row_count"`
	ColCount      int                   `json:"col_count"`
	HeaderRow     int                   `json:"header_row"`
	Columns       []string              `json:"columns"`
	SuggestedSkip bool                  `json:"suggested_skip"`
	SkipReason    string                `json:"skip_reason"`
	Plan          *sheetparse.SheetPlan `json:"plan,omitempty"`
	Stats         *SheetPreviewStat     `json:"stats,omitempty"`
}

func (s *ImportSheetService) batchDir(batchID string) string {
	return filepath.Join(s.dataDir, "tmp", "import", batchID)
}

func (s *ImportSheetService) manifestPath(batchID string) string {
	return filepath.Join(s.batchDir(batchID), "manifest.json")
}

func (s *ImportSheetService) rowsPath(batchID, group string) string {
	return filepath.Join(s.batchDir(batchID), "rows-"+group+".jsonl")
}

// writeManifest 原子写 manifest（临时文件 + rename）。
func (s *ImportSheetService) writeManifest(m *Manifest) error {
	buf, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	dst := s.manifestPath(m.BatchID)
	tmp := dst + ".tmp"
	if err = os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// loadManifest 读取并校验批次（存在性 + 24h 有效期 + 幂等状态）。
func (s *ImportSheetService) loadManifest(batchID string, allowCommitted bool) (*Manifest, error) {
	if strings.TrimSpace(batchID) == "" {
		return nil, apierr.New(apierr.CodeValidation, "batch_id 必填")
	}
	raw, err := os.ReadFile(s.manifestPath(batchID))
	if err != nil {
		return nil, apierr.New(apierr.CodeImportBatchExpired, "导入批次不存在或已过期，请重新上传").
			WithDetails(map[string]any{"batch_id": batchID})
	}
	var m Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return nil, apierr.New(apierr.CodeImportBatchExpired, "导入批次文件损坏，请重新上传")
	}
	if timeutil.NowUnix()-m.CreatedAt > int64(importBatchTTL.Seconds()) {
		return nil, apierr.New(apierr.CodeImportBatchExpired, "导入批次已超过 24 小时，请重新上传").
			WithDetails(map[string]any{"batch_id": batchID, "created_at": timeutil.FormatLocal(m.CreatedAt)})
	}
	if !allowCommitted {
		if b, gerr := s.batches.Get(batchID); gerr == nil && b != nil && b.Status == "committed" {
			return nil, apierr.New(apierr.CodeImportBatchCommitted, "该批次已提交，请勿重复提交").
				WithDetails(map[string]any{"batch_id": batchID,
					"committed_at": timeutil.FormatLocalPtr(b.CommittedAt)})
		}
	}
	return &m, nil
}

// findFile 按 file_id 定位清单文件。
func (m *Manifest) findFile(fileID string) *ManifestFile {
	for i := range m.Files {
		if m.Files[i].FileID == fileID {
			return &m.Files[i]
		}
	}
	return nil
}

// findSheet 按 (file_id, sheet_index) 定位清单工作表。
func (m *Manifest) findSheet(fileID string, sheetIndex int) *ManifestSheet {
	f := m.findFile(fileID)
	if f == nil {
		return nil
	}
	for i := range f.Sheets {
		if f.Sheets[i].SheetIndex == sheetIndex {
			return &f.Sheets[i]
		}
	}
	return nil
}

// fileData 读取原始上传文件字节。
func (s *ImportSheetService) fileData(m *Manifest, f *ManifestFile) ([]byte, error) {
	path := filepath.Join(s.batchDir(m.BatchID), filepath.FromSlash(f.StoreName))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, apierr.New(apierr.CodeImportBatchExpired, "批次原始文件已丢失，请重新上传").
			WithDetails(map[string]any{"file_id": f.FileID})
	}
	return data, nil
}

// ---------------- Probe ----------------

// ProbeFileInput 一个待探测的上传文件。
type ProbeFileInput struct {
	FileName string
	Data     []byte
}

// ProbeFileDTO 单文件探测结果。
type ProbeFileDTO struct {
	FileID     string          `json:"file_id"`
	FileName   string          `json:"file_name"`
	FileSize   int64           `json:"file_size"`
	SHA1       string          `json:"sha1"`
	Kind       string          `json:"kind"`
	Encoding   string          `json:"encoding"`
	SheetCount int             `json:"sheet_count"`
	Sheets     []ProbeSheetDTO `json:"sheets"`
}

// ProbeSheetDTO 单工作表探测结果（§2.2）。
type ProbeSheetDTO struct {
	SheetIndex          int               `json:"sheet_index"`
	SheetName           string            `json:"sheet_name"`
	RowCount            int               `json:"row_count"`
	ColCount            int               `json:"col_count"`
	HeaderRow           int               `json:"header_row"`
	HeaderRowCandidates []int             `json:"header_row_candidates"`
	Columns             []string          `json:"columns"`
	SampleRows          [][]string        `json:"sample_rows"`
	SuggestedMapping    map[string]string `json:"suggested_mapping"`
	SuggestedDirection  string            `json:"suggested_direction"`
	SuggestedSkip       bool              `json:"suggested_skip"`
	SkipReason          string            `json:"skip_reason"`
	DateHasTime         bool              `json:"date_has_time"`
	Warning             string            `json:"warning,omitempty"`
}

// ProbeResult POST /api/import/sheet/probe 响应。
type ProbeResult struct {
	BatchID  string         `json:"batch_id"`
	Files    []ProbeFileDTO `json:"files"`
	Warnings []string       `json:"warnings"`
}

// Probe 上传探测：写批次目录 + manifest + import_batches，返回 sheet 列表与映射建议。
func (s *ImportSheetService) Probe(ctx context.Context, inputs []ProbeFileInput) (*ProbeResult, error) {
	if len(inputs) == 0 {
		return nil, apierr.New(apierr.CodeValidation, "files 必填（至少 1 个 .xlsx/.xlsm/.csv）")
	}
	if len(inputs) > 20 {
		return nil, apierr.New(apierr.CodeValidation, "单次最多上传 20 个文件")
	}
	total := 0
	for _, in := range inputs {
		total += len(in.Data)
	}
	if total > sheetparse.MaxBatchSize {
		return nil, apierr.Newf(apierr.CodeImportTooLarge,
			"本批文件合计 %d 字节，超过 %d 字节上限，请分批导入", total, sheetparse.MaxBatchSize)
	}

	batchID := ids.ShortID("imp_")
	dir := s.batchDir(batchID)
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
		return nil, fmt.Errorf("创建批次目录失败: %w", err)
	}
	m := &Manifest{BatchID: batchID, CreatedAt: timeutil.NowUnix(), Files: []ManifestFile{}, Warnings: []string{}}
	res := &ProbeResult{BatchID: batchID, Files: []ProbeFileDTO{}, Warnings: []string{}}

	for idx, in := range inputs {
		meta, err := sheetparse.ProbeFile(in.Data, in.FileName, 8)
		if err != nil {
			return nil, err
		}
		fileID := ids.ShortID("fs_")
		storeName := filepath.ToSlash(filepath.Join("files", fileID+safeExt(in.FileName)))
		if err = os.WriteFile(filepath.Join(dir, filepath.FromSlash(storeName)), in.Data, 0o644); err != nil {
			return nil, fmt.Errorf("保存上传文件失败: %w", err)
		}
		mf := ManifestFile{
			FileID: fileID, Index: idx, FileName: in.FileName, Size: meta.Size, SHA1: meta.SHA1,
			Kind: meta.Kind, Encoding: meta.Encoding, Date1904: meta.Date1904, StoreName: storeName,
			Sheets: []ManifestSheet{},
		}
		dto := ProbeFileDTO{
			FileID: fileID, FileName: in.FileName, FileSize: meta.Size, SHA1: meta.SHA1,
			Kind: meta.Kind, Encoding: meta.Encoding, SheetCount: len(meta.Sheets),
			Sheets: make([]ProbeSheetDTO, 0, len(meta.Sheets)),
		}
		for _, sh := range meta.Sheets {
			mf.Sheets = append(mf.Sheets, ManifestSheet{
				SheetIndex: sh.SheetIndex, SheetName: sh.SheetName, RowCount: sh.RowCount,
				ColCount: sh.ColCount, HeaderRow: sh.HeaderRow, Columns: sh.Columns,
				SuggestedSkip: sh.SuggestedSkip, SkipReason: sh.SkipReason,
			})
			dto.Sheets = append(dto.Sheets, ProbeSheetDTO{
				SheetIndex: sh.SheetIndex, SheetName: sh.SheetName, RowCount: sh.RowCount,
				ColCount: sh.ColCount, HeaderRow: sh.HeaderRow,
				HeaderRowCandidates: sh.HeaderRowCandidates, Columns: sh.Columns,
				SampleRows: sh.SampleRows, SuggestedMapping: sh.SuggestedMapping,
				SuggestedDirection: sh.SuggestedDirection, SuggestedSkip: sh.SuggestedSkip,
				SkipReason: sh.SkipReason, DateHasTime: sh.DateHasTime, Warning: sh.Warning,
			})
			if sh.SuggestedSkip {
				msg := fmt.Sprintf("「%s」%s，已建议不导入", sh.SheetName, sh.SkipReason)
				res.Warnings = append(res.Warnings, msg)
			}
			if sh.Warning != "" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("「%s」%s", sh.SheetName, sh.Warning))
			}
		}
		m.Files = append(m.Files, mf)
		res.Files = append(res.Files, dto)
	}

	if err := s.writeManifest(m); err != nil {
		return nil, err
	}
	sheetCount := 0
	for _, f := range m.Files {
		sheetCount += len(f.Sheets)
	}
	if err := s.batches.Create(&store.ImportBatch{
		ID: batchID, Kind: "sheet", Status: "previewed", FileCount: len(m.Files),
		SheetCount: sheetCount, Encoding: m.Files[0].Encoding,
		ManifestPath: s.manifestPath(batchID),
	}); err != nil {
		return nil, err
	}
	return res, nil
}

// safeExt 保留原扩展名（仅白名单后缀，避免路径注入）。
func safeExt(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".xlsx", ".xlsm", ".csv", ".txt":
		return ext
	}
	return ".bin"
}

// ---------------- Preview ----------------

// SheetPlanInput 单表预览配置（§2.3）。
type SheetPlanInput struct {
	FileID        string                   `json:"file_id"`
	SheetIndex    int                      `json:"sheet_index"`
	HeaderRow     int                      `json:"header_row"`
	Skip          bool                     `json:"skip"`
	Mapping       sheetparse.Mapping       `json:"mapping"`
	DirectionRule sheetparse.DirectionRule `json:"direction_rule"`
	CategorySplit *string                  `json:"category_split"`
	TimePrecision string                   `json:"time_precision"`
}

// PreviewDefaults 预览默认值。
type PreviewDefaults struct {
	AccountID string `json:"account_id"`
}

// PreviewInput POST /api/import/sheet/preview 请求体。
type PreviewInput struct {
	BatchID  string           `json:"batch_id"`
	Defaults PreviewDefaults  `json:"defaults"`
	Sheets   []SheetPlanInput `json:"sheets"`
}

// TimeDistSample 同日时间分布样例。
type TimeDistSample struct {
	LocalDate string   `json:"local_date"`
	Count     int      `json:"count"`
	Times     []string `json:"times"`
}

// SheetPreviewStat 单表预览统计。
type SheetPreviewStat struct {
	FileID                 string                `json:"file_id"`
	SheetIndex             int                   `json:"sheet_index"`
	SheetName              string                `json:"sheet_name"`
	TotalRows              int                   `json:"total_rows"`
	ParsedOK               int                   `json:"parsed_ok"`
	Skipped                int                   `json:"skipped"`
	Error                  int                   `json:"error"`
	DateRange              []string              `json:"date_range"`
	DirectionStats         map[string]int        `json:"direction_stats"`
	TimeDistributionSample []TimeDistSample      `json:"time_distribution_sample"`
	Errors                 []sheetparse.RowError `json:"errors"`
}

// AutoCreateCategory 自动新增分类清单项。
type AutoCreateCategory struct {
	Name            string  `json:"name"`
	ParentName      *string `json:"parent_name"`
	TransactionType string  `json:"transaction_type"`
	Level           int     `json:"level"`
	Rows            int     `json:"rows"`
	Exists          bool    `json:"exists"`
}

// AutoCreateTag 自动新增标签清单项。
type AutoCreateTag struct {
	Name   string `json:"name"`
	Rows   int    `json:"rows"`
	Exists bool   `json:"exists"`
}

// AutoCreateStat 自动新增清单。
type AutoCreateStat struct {
	Categories []AutoCreateCategory `json:"categories"`
	Tags       []AutoCreateTag      `json:"tags"`
}

// ParsedRowDTO 预览行解析结果。
type ParsedRowDTO struct {
	TransactionTime string   `json:"transaction_time"`
	LocalDate       string   `json:"local_date"`
	TransactionType string   `json:"transaction_type"`
	AmountCents     int64    `json:"amount_cents"`
	AccountID       string   `json:"account_id"`
	CategoryName    *string  `json:"category_name"`
	CategoryID      *string  `json:"category_id"`
	SubCategoryName *string  `json:"sub_category_name"`
	SubCategoryID   *string  `json:"sub_category_id"`
	TagNames        []string `json:"tag_names"`
	MerchantText    string   `json:"merchant_text"`
	Remark          string   `json:"remark"`
	ExternalID      string   `json:"external_id"`
	TimePrecision   string   `json:"time_precision"`
}

// PreviewRow 预览行。
type PreviewRow struct {
	RowNo       int               `json:"row_no"`
	SheetIndex  int               `json:"sheet_index"`
	FileID      string            `json:"file_id"`
	RowKey      string            `json:"row_key"`
	Raw         map[string]string `json:"raw"`
	Parsed      *ParsedRowDTO     `json:"parsed"`
	Dup         ImportDup         `json:"dup"`
	SoftDup     *ImportDup        `json:"soft_dup,omitempty"`
	Frozen      bool              `json:"frozen"`
	Warnings    []string          `json:"warnings"`
	NeedConfirm []string          `json:"need_confirm"`
}

// PreviewSummary 预览汇总。
type PreviewSummary struct {
	TotalRows    int `json:"total_rows"`
	ParsedOK     int `json:"parsed_ok"`
	Skipped      int `json:"skipped"`
	Error        int `json:"error"`
	DupCount     int `json:"dup_count"`
	SoftDupCount int `json:"soft_dup_count"`
	FrozenCount  int `json:"frozen_count"`
}

// PreviewResult POST /api/import/sheet/preview 响应。
type PreviewResult struct {
	BatchID       string             `json:"batch_id"`
	Summary       PreviewSummary     `json:"summary"`
	Sheets        []SheetPreviewStat `json:"sheets"`
	AutoCreate    AutoCreateStat     `json:"auto_create"`
	Rows          []PreviewRow       `json:"rows"`
	RowsTruncated bool               `json:"rows_truncated"`
}

// parseOutput 全量解析结果。
type parseOutput struct {
	rows    []*sheetparse.ParsedRow
	errors  []sheetparse.RowError
	stats   map[string]*SheetPreviewStat // key: fileID|sheetIndex
	order   []string                     // stats 的稳定顺序
	total   int
	skipped int
}

// planKey 单表定位键。
func planKey(fileID string, sheetIndex int) string {
	return fmt.Sprintf("%s|%d", fileID, sheetIndex)
}

// buildPlan 校验并构造解析计划（§2.3 必填校验）。
func buildPlan(m *Manifest, in *SheetPlanInput) (*sheetparse.SheetPlan, error) {
	f := m.findFile(in.FileID)
	if f == nil {
		return nil, apierr.New(apierr.CodeImportSheetInvalid, "file_id 不存在于该批次").
			WithDetails(map[string]any{"file_id": in.FileID})
	}
	var sh *ManifestSheet
	for i := range f.Sheets {
		if f.Sheets[i].SheetIndex == in.SheetIndex {
			sh = &f.Sheets[i]
			break
		}
	}
	if sh == nil {
		return nil, apierr.Newf(apierr.CodeImportSheetInvalid,
			"sheet_index %d 不存在（文件「%s」共 %d 张表）", in.SheetIndex, f.FileName, len(f.Sheets)).
			WithDetails(map[string]any{"file_id": in.FileID, "sheet_index": in.SheetIndex})
	}
	headerRow := in.HeaderRow
	if headerRow <= 0 {
		headerRow = sh.HeaderRow
	}
	if headerRow <= 0 {
		headerRow = 1
	}
	if sh.RowCount > 0 && headerRow > sh.RowCount {
		return nil, apierr.Newf(apierr.CodeImportSheetInvalid,
			"header_row %d 越界（「%s」共 %d 行）", headerRow, sh.SheetName, sh.RowCount).
			WithDetails(map[string]any{"sheet_index": in.SheetIndex})
	}
	plan := &sheetparse.SheetPlan{
		FileID: in.FileID, SheetIndex: in.SheetIndex, SheetName: sh.SheetName,
		HeaderRow: headerRow, Skip: in.Skip, Mapping: in.Mapping,
		DirectionRule: in.DirectionRule, TimePrecision: in.TimePrecision,
	}
	if in.CategorySplit != nil {
		plan.CategorySplit = *in.CategorySplit
	}
	if plan.TimePrecision != sheetparse.PrecisionDay {
		plan.TimePrecision = sheetparse.PrecisionSecond
	}
	if !plan.Skip {
		if plan.Mapping.Date == "" && plan.Mapping.Month == "" {
			return nil, apierr.New(apierr.CodeImportSheetInvalid,
				"必须映射日期列（date 或 month）").
				WithDetails(map[string]any{"file_id": in.FileID, "sheet_index": in.SheetIndex})
		}
		if plan.Mapping.Amount == "" {
			return nil, apierr.New(apierr.CodeImportSheetInvalid, "必须映射金额列（amount）").
				WithDetails(map[string]any{"file_id": in.FileID, "sheet_index": in.SheetIndex})
		}
	}
	return plan, nil
}

// parsePlans 按计划全量解析（流式读原文件，不持有整表）。
func (s *ImportSheetService) parsePlans(m *Manifest, plans []*sheetparse.SheetPlan) (*parseOutput, error) {
	out := &parseOutput{stats: map[string]*SheetPreviewStat{}, order: []string{}}
	for _, plan := range plans {
		if plan.Skip {
			continue
		}
		f := m.findFile(plan.FileID)
		if f == nil {
			return nil, apierr.New(apierr.CodeImportSheetInvalid, "file_id 不存在于该批次")
		}
		data, err := s.fileData(m, f)
		if err != nil {
			return nil, err
		}
		table, err := sheetparse.OpenTable(data, f.Kind, plan.SheetIndex)
		if err != nil {
			return nil, err
		}
		key := planKey(plan.FileID, plan.SheetIndex)
		sheet := m.findSheet(plan.FileID, plan.SheetIndex)
		if sheet == nil {
			return nil, apierr.New(apierr.CodeImportSheetInvalid, "sheet_index 不存在于该批次文件")
		}
		st := &SheetPreviewStat{
			FileID: plan.FileID, SheetIndex: plan.SheetIndex, SheetName: plan.SheetName,
			DirectionStats: map[string]int{}, TimeDistributionSample: []TimeDistSample{},
			Errors: []sheetparse.RowError{},
		}
		if _, ok := out.stats[key]; !ok {
			out.order = append(out.order, key)
		}
		out.stats[key] = st

		var parser *sheetparse.RowParser
		var columns []string
		for {
			row, rowNo, ok, rerr := table.Next()
			if rerr != nil {
				table.Close()
				return nil, fmt.Errorf("读取「%s」第 %d 行失败: %w", plan.SheetName, rowNo, rerr)
			}
			if !ok {
				break
			}
			if rowNo < plan.HeaderRow {
				continue // 表头之上的标题/说明行
			}
			if rowNo == plan.HeaderRow {
				columns = row
				parser = sheetparse.NewRowParser(columns, plan, f.Date1904)
				continue
			}
			if parser == nil {
				// 表头行为空（探测时也拿不到列名）→ 用清单里的列名兜底
				columns = sheet.Columns
				if len(columns) == 0 {
					table.Close()
					return nil, apierr.New(apierr.CodeImportSheetInvalid,
						"无法读取表头行，请重新指定 header_row").
						WithDetails(map[string]any{"file_id": plan.FileID,
							"sheet_index": plan.SheetIndex, "header_row": plan.HeaderRow})
				}
				parser = sheetparse.NewRowParser(columns, plan, f.Date1904)
			}
			if len(row) == 0 {
				continue // 空行不计入统计
			}
			st.TotalRows++
			out.total++
			pr, perr := parser.Parse(row, rowNo, f.SHA1)
			if perr != nil {
				st.Error++
				if len(st.Errors) < 50 {
					st.Errors = append(st.Errors, *perr)
				}
				out.errors = append(out.errors, *perr)
				continue
			}
			pr.FileIndex = f.Index
			st.ParsedOK++
			st.DirectionStats[pr.Type]++
			out.rows = append(out.rows, pr)
			if len(out.rows) > sheetparse.MaxBatchRows {
				table.Close()
				return nil, apierr.Newf(apierr.CodeImportTooLarge,
					"本批解析行数超过 %d 行上限，请拆分文件后分批导入", sheetparse.MaxBatchRows)
			}
		}
		table.Close()
	}
	// 稳定排序：file_index → sheet_index → row_no（§4.1 序号 i 的定义）
	sort.SliceStable(out.rows, func(i, j int) bool {
		a, b := out.rows[i], out.rows[j]
		if a.FileIndex != b.FileIndex {
			return a.FileIndex < b.FileIndex
		}
		if a.SheetIndex != b.SheetIndex {
			return a.SheetIndex < b.SheetIndex
		}
		return a.RowNo < b.RowNo
	})
	return out, nil
}

// applyDayDistribution 对"无时分秒"的行按 local_date 分组平均分布（§4.1）。
// rows 必须已按 (file_index, sheet_index, row_no) 排序。
func applyDayDistribution(rows []*sheetparse.ParsedRow) {
	groups := map[string][]int{}
	var order []string
	for i, r := range rows {
		if r.HasTime {
			continue
		}
		if _, ok := groups[r.LocalDate]; !ok {
			order = append(order, r.LocalDate)
		}
		groups[r.LocalDate] = append(groups[r.LocalDate], i)
	}
	for _, date := range order {
		idx := groups[date]
		n := len(idx)
		dayStart, err := timeutil.ParseDateStart(date)
		if err != nil {
			continue
		}
		for k, ri := range idx {
			rows[ri].TimeUnix = dayStart + int64(sheetparse.DistributeOffset(k, n))
			if sheetparse.DistributeOverflowWarning(n) {
				rows[ri].Warnings = append(rows[ri].Warnings, "当日记录超过 86400 条，时间仅用于排序")
			}
		}
	}
}

// softDupKey 软重复键（§4.6 L2）。
func softDupKey(localDate string, amountCents int64, merchant, typ string) string {
	return fmt.Sprintf("%s|%d|%s|%s", localDate, amountCents, merchant, typ)
}

// fetchExistingExternalIDs 分块查询已存在的 external_id。
func (s *ImportSheetService) fetchExistingExternalIDs(externalIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	const chunk = 400
	for i := 0; i < len(externalIDs); i += chunk {
		end := i + chunk
		if end > len(externalIDs) {
			end = len(externalIDs)
		}
		part := externalIDs[i:end]
		q := `SELECT external_id FROM transactions WHERE external_id IN ` + placeholders(len(part))
		rows, err := s.s.SQL.Query(q, toAnyArgs(part)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = true
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// fetchSoftDupIndex 预取批次日期的既有交易，构建软重复索引（避免逐行查询）。
func (s *ImportSheetService) fetchSoftDupIndex(dates []string, accountID string) (map[string]string, error) {
	out := map[string]string{}
	const chunk = 200
	for i := 0; i < len(dates); i += chunk {
		end := i + chunk
		if end > len(dates) {
			end = len(dates)
		}
		part := dates[i:end]
		q := `SELECT id, local_date, amount_cents, IFNULL(merchant_text,''), transaction_type
			FROM transactions WHERE deleted_at IS NULL AND account_id = ?
			  AND local_date IN ` + placeholders(len(part)) +
			` ORDER BY created_seq ASC`
		args := append([]any{accountID}, toAnyArgs(part)...)
		rows, err := s.s.SQL.Query(q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, localDate, merchant, typ string
			var amount int64
			if err = rows.Scan(&id, &localDate, &amount, &merchant, &typ); err != nil {
				rows.Close()
				return nil, err
			}
			k := softDupKey(localDate, amount, merchant, typ)
			if _, ok := out[k]; !ok {
				out[k] = id
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// dedupAnnotate 标注精确重复（L1）与软重复（L2）。
func (s *ImportSheetService) dedupAnnotate(rows []*sheetparse.ParsedRow, accountID string,
	dupIdx map[int]ImportDup, softIdx map[int]*ImportDup) error {
	extIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.ExternalID != "" {
			extIDs = append(extIDs, r.ExternalID)
		}
	}
	existing, err := s.fetchExistingExternalIDs(extIDs)
	if err != nil {
		return err
	}
	// 先标记精确重复，软重复只对非精确重复行计算
	dateSet := map[string]bool{}
	for i, r := range rows {
		if r.ExternalID != "" && existing[r.ExternalID] {
			dupIdx[i] = ImportDup{IsDup: true, Reason: "external_id"}
			continue
		}
		dateSet[r.LocalDate] = true
	}
	dates := make([]string, 0, len(dateSet))
	for d := range dateSet {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	if len(dates) == 0 {
		return nil
	}
	softIndex, err := s.fetchSoftDupIndex(dates, accountID)
	if err != nil {
		return err
	}
	for i, r := range rows {
		if _, isDup := dupIdx[i]; isDup {
			continue
		}
		if matched, ok := softIndex[softDupKey(r.LocalDate, r.AmountCents, r.MerchantText, r.Type)]; ok {
			softIdx[i] = &ImportDup{IsDup: true, Reason: "same_content", MatchedTransactionID: &matched}
		}
	}
	return nil
}

// Preview 映射解析预览（全量落盘 + 前 200 行返回）。
func (s *ImportSheetService) Preview(ctx context.Context, in *PreviewInput) (*PreviewResult, error) {
	if in == nil || len(in.Sheets) == 0 {
		return nil, apierr.New(apierr.CodeImportSheetInvalid, "sheets 必填（至少 1 张表）")
	}
	m, err := s.loadManifest(in.BatchID, false)
	if err != nil {
		return nil, err
	}
	accountID, err := s.resolveAccountID(in.Defaults.AccountID)
	if err != nil {
		return nil, err
	}
	plans := make([]*sheetparse.SheetPlan, 0, len(in.Sheets))
	for i := range in.Sheets {
		plan, perr := buildPlan(m, &in.Sheets[i])
		if perr != nil {
			return nil, perr
		}
		plans = append(plans, plan)
	}
	// 计划落盘（commit 只带 file_id + sheet_index）
	for i := range m.Files {
		for j := range m.Files[i].Sheets {
			for _, plan := range plans {
				if m.Files[i].FileID == plan.FileID && m.Files[i].Sheets[j].SheetIndex == plan.SheetIndex {
					m.Files[i].Sheets[j].Plan = plan
					m.Files[i].Sheets[j].HeaderRow = plan.HeaderRow
				}
			}
		}
	}

	out, err := s.parsePlans(m, plans)
	if err != nil {
		return nil, err
	}
	applyDayDistribution(out.rows)

	boundary := s.balance.FreezeBoundary(time.Now().Unix())
	dupIdx := map[int]ImportDup{}
	softIdx := map[int]*ImportDup{}
	if err = s.dedupAnnotate(out.rows, accountID, dupIdx, softIdx); err != nil {
		return nil, err
	}

	res := &PreviewResult{
		BatchID: m.BatchID, Sheets: make([]SheetPreviewStat, 0, len(out.order)),
		Rows: []PreviewRow{}, AutoCreate: AutoCreateStat{
			Categories: []AutoCreateCategory{}, Tags: []AutoCreateTag{},
		},
	}
	frozenCount := 0
	res.Summary.TotalRows = out.total
	res.Summary.ParsedOK = len(out.rows)
	res.Summary.Error = len(out.errors)
	res.Summary.Skipped = out.total - len(out.rows) - len(out.errors)
	if res.Summary.Skipped < 0 {
		res.Summary.Skipped = 0
	}
	for i, r := range out.rows {
		if r.TimeUnix < boundary {
			frozenCount++
		}
		if i < importPreviewRows {
			pr := PreviewRow{
				RowNo: r.RowNo, SheetIndex: r.SheetIndex, FileID: r.FileID, RowKey: r.RowKey,
				Raw: r.Raw, Dup: dupIdx[i], Frozen: r.TimeUnix < boundary,
				Warnings: append([]string{}, r.Warnings...), NeedConfirm: []string{},
			}
			pr.Parsed = toParsedRowDTO(r, accountID)
			if pr.Dup.IsDup {
				res.Summary.DupCount++
			}
			if sd, ok := softIdx[i]; ok {
				pr.SoftDup = sd
				res.Summary.SoftDupCount++
			}
			if pr.Frozen {
				pr.Warnings = append(pr.Warnings, "交易时间落在冻结区，确认后不可直接修改")
			}
			res.Rows = append(res.Rows, pr)
		} else {
			if dupIdx[i].IsDup {
				res.Summary.DupCount++
			}
			if _, ok := softIdx[i]; ok {
				res.Summary.SoftDupCount++
			}
		}
	}
	res.Summary.FrozenCount = frozenCount
	res.RowsTruncated = len(out.rows) > importPreviewRows

	// 表级统计（日期区间 + 时间分布样例）
	for _, key := range out.order {
		st := out.stats[key]
		fillStatExtremes(st, out.rows)
		res.Sheets = append(res.Sheets, *st)
	}

	// 自动新增分类/标签清单（只统计，不写库）
	if err = s.fillAutoCreate(&res.AutoCreate, out.rows, plans); err != nil {
		return nil, err
	}

	// 全量落盘（commit 用：校验时间分布是否漂移）
	if err = s.writePreviewRows(m.BatchID, out.rows); err != nil {
		return nil, err
	}
	// 回写 manifest 与批次统计
	for i := range m.Files {
		for j := range m.Files[i].Sheets {
			key := planKey(m.Files[i].FileID, m.Files[i].Sheets[j].SheetIndex)
			if st, ok := out.stats[key]; ok {
				m.Files[i].Sheets[j].Stats = st
			}
		}
	}
	m.Warnings = append(m.Warnings, out.errorsPreview()...)
	if err = s.writeManifest(m); err != nil {
		return nil, err
	}
	if err = s.batches.UpdateCounts(m.BatchID, store.BatchCounts{
		SheetCount: len(plans), TotalRows: int64(res.Summary.TotalRows),
		CreatedRows: 0, SkippedRows: int64(res.Summary.Skipped),
		ErrorRows: int64(res.Summary.Error),
	}); err != nil {
		return nil, err
	}
	return res, nil
}

// previewJSONL 预览落盘行（commit 对比时间分布用）。
type previewJSONL struct {
	RowKey    string `json:"k"`
	TimeUnix  int64  `json:"t"`
	LocalDate string `json:"d"`
	Precision string `json:"p"`
}

// writePreviewRows 全量写 rows-<batch>.jsonl。
func (s *ImportSheetService) writePreviewRows(batchID string, rows []*sheetparse.ParsedRow) error {
	path := s.rowsPath(batchID, "preview")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 64*1024)
	enc := json.NewEncoder(w)
	for _, r := range rows {
		if err = enc.Encode(previewJSONL{
			RowKey: r.RowKey, TimeUnix: r.TimeUnix, LocalDate: r.LocalDate, Precision: r.TimePrecision,
		}); err != nil {
			return err
		}
	}
	return w.Flush()
}

// readPreviewTimes 读取预览时的分布时间（row_key → unix）。
func (s *ImportSheetService) readPreviewTimes(batchID string) (map[string]int64, error) {
	out := map[string]int64{}
	f, err := os.Open(s.rowsPath(batchID, "preview"))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec previewJSONL
		if err = json.Unmarshal([]byte(line), &rec); err != nil {
			return out, nil // 半行/损坏时放弃比对（仅用于提示）
		}
		out[rec.RowKey] = rec.TimeUnix
	}
	return out, sc.Err()
}

// fillStatExtremes 填充单表日期区间、时间分布样例与跳过计数。
func fillStatExtremes(st *SheetPreviewStat, rows []*sheetparse.ParsedRow) {
	var dates []string
	byDate := map[string][]string{}
	for _, r := range rows {
		if r.FileID != st.FileID || r.SheetIndex != st.SheetIndex {
			continue
		}
		dates = append(dates, r.LocalDate)
		byDate[r.LocalDate] = append(byDate[r.LocalDate], timeutil.FormatLocal(r.TimeUnix))
	}
	st.Skipped = st.TotalRows - st.ParsedOK - st.Error
	if len(dates) == 0 {
		st.DateRange = []string{}
		return
	}
	sort.Strings(dates)
	st.DateRange = []string{dates[0], dates[len(dates)-1]}
	// 取同日多条（>1）的最多 N 个日期作为样例
	var multi []string
	for d, times := range byDate {
		if len(times) > 1 {
			multi = append(multi, d)
		}
	}
	sort.Strings(multi)
	if len(multi) > importSampleTimes {
		multi = multi[:importSampleTimes]
	}
	for _, d := range multi {
		st.TimeDistributionSample = append(st.TimeDistributionSample, TimeDistSample{
			LocalDate: d, Count: len(byDate[d]), Times: byDate[d],
		})
	}
}

// errorsPreview 汇总错误提示（写入 manifest.warnings，便于追溯）。
func (o *parseOutput) errorsPreview() []string {
	if len(o.errors) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("解析失败 %d 行（可在预览中单行修正后重试）", len(o.errors))}
}

// toParsedRowDTO ParsedRow → DTO（category_id 命中已有分类时回填）。
func toParsedRowDTO(r *sheetparse.ParsedRow, accountID string) *ParsedRowDTO {
	dto := &ParsedRowDTO{
		TransactionTime: timeutil.FormatLocal(r.TimeUnix),
		LocalDate:       r.LocalDate,
		TransactionType: r.Type,
		AmountCents:     r.AmountCents,
		AccountID:       accountID,
		TagNames:        r.TagNames,
		MerchantText:    r.MerchantText,
		Remark:          r.Remark,
		ExternalID:      r.ExternalID,
		TimePrecision:   r.TimePrecision,
	}
	if r.CategoryName != "" {
		name := r.CategoryName
		dto.CategoryName = &name
	}
	if r.SubCategoryName != "" {
		name := r.SubCategoryName
		dto.SubCategoryName = &name
	}
	if dto.TagNames == nil {
		dto.TagNames = []string{}
	}
	return dto
}

// fillAutoCreate 统计需要自动创建的分类/标签（preview 只读，不写库）。
func (s *ImportSheetService) fillAutoCreate(res *AutoCreateStat, rows []*sheetparse.ParsedRow,
	plans []*sheetparse.SheetPlan) error {
	splitByKey := map[string]string{}
	for _, p := range plans {
		splitByKey[planKey(p.FileID, p.SheetIndex)] = p.CategorySplit
	}
	catRows := map[string]int{}
	catType := map[string]string{}
	catOrder := []string{}
	tagRows := map[string]int{}
	tagOrder := []string{}
	for _, r := range rows {
		if r.CategoryName != "" {
			sep := splitByKey[planKey(r.FileID, r.SheetIndex)]
			l1, l2 := sheetparse.SplitCategory(r.CategoryName, sep)
			if l1 != "" {
				key := CategoryKey("", l1)
				if _, ok := catRows[key]; !ok {
					catOrder = append(catOrder, key)
				}
				catRows[key]++
				catType[key] = r.Type
			}
			if l2 != "" {
				key := CategoryKey(l1, l2)
				if _, ok := catRows[key]; !ok {
					catOrder = append(catOrder, key)
				}
				catRows[key]++
				catType[key] = r.Type
			}
		}
		for _, t := range r.TagNames {
			if _, ok := tagRows[t]; !ok {
				tagOrder = append(tagOrder, t)
			}
			tagRows[t]++
		}
	}
	for _, key := range catOrder {
		parts := strings.SplitN(key, "\x00", 2)
		parent, name := parts[0], parts[1]
		_, existingType, found, err := LookupCategory(s.s.SQL, parent, name)
		if err != nil {
			return err
		}
		item := AutoCreateCategory{
			Name: name, TransactionType: catType[key], Rows: catRows[key], Exists: found,
			Level: 1,
		}
		if parent != "" {
			item.ParentName = &parent
			item.Level = 2
		}
		if found && existingType != "both" && existingType != catType[key] {
			item.TransactionType = existingType
		}
		res.Categories = append(res.Categories, item)
	}
	for _, name := range tagOrder {
		exists, err := TagExists(s.s.SQL, name)
		if err != nil {
			return err
		}
		res.Tags = append(res.Tags, AutoCreateTag{Name: name, Rows: tagRows[name], Exists: exists})
	}
	return nil
}

// resolveAccountID 解析默认账户（空 → meta.default_account_id）。
func (s *ImportSheetService) resolveAccountID(accountID string) (string, error) {
	if accountID == "" {
		if v, err := s.meta.Get("default_account_id"); err == nil {
			accountID = v
		}
	}
	if accountID == "" {
		return "", apierr.New(apierr.CodeValidation, "defaults.account_id 必填（或先在设置里指定默认账户）")
	}
	if _, err := s.master.GetAccount(accountID); err != nil {
		return "", apierr.New(apierr.CodeValidation, "defaults.account_id 不存在").
			WithDetails(map[string]any{"account_id": accountID})
	}
	return accountID, nil
}

// ---------------- Commit ----------------

// CommitSheetRef 提交的表的定位（file_id + sheet_index）。
type CommitSheetRef struct {
	FileID     string `json:"file_id"`
	SheetIndex int    `json:"sheet_index"`
}

// CommitDefaults 提交默认值（§2.4）。
type CommitDefaults struct {
	AccountID     string   `json:"account_id"`
	CategoryID    *string  `json:"category_id"`
	TagIDs        []string `json:"tag_ids"`
	TimePrecision string   `json:"time_precision"`
}

// CommitInput POST /api/import/sheet/commit 请求体。
type CommitInput struct {
	BatchID                 string           `json:"batch_id"`
	Sheets                  []CommitSheetRef `json:"sheets"`
	RowKeys                 []string         `json:"row_keys"`
	Defaults                CommitDefaults   `json:"defaults"`
	CreateMissingCategories bool             `json:"create_missing_categories"`
	CreateMissingTags       bool             `json:"create_missing_tags"`
	SkipDuplicates          bool             `json:"skip_duplicates"`
	SkipSoftDuplicates      bool             `json:"skip_soft_duplicates"`
	AllowFrozen             bool             `json:"allow_frozen"`
	// FreezeMode 冻结策略（§9.7）：'defer'（默认，早于边界 → 待确认可写）| 'immediate'。
	FreezeMode string `json:"freeze_mode"`
}

// CommitResult POST /api/import/sheet/commit 响应（§2.4）。
type CommitResult struct {
	BatchID                      string       `json:"batch_id"`
	Created                      int          `json:"created"`
	SkippedDup                   int          `json:"skipped_dup"`
	SkippedSoftDup               int          `json:"skipped_soft_dup"`
	SkippedInvalid               int          `json:"skipped_invalid"`
	FrozenCreated                int          `json:"frozen_created"`
	RematerializeTriggered       bool         `json:"rematerialize_triggered"`
	CreatedCategories            []CreatedRef `json:"created_categories"`
	CreatedTags                  []CreatedRef `json:"created_tags"`
	TransactionIDs               []string     `json:"transaction_ids"`
	TransactionIDsOmitted        bool         `json:"transaction_ids_omitted"`
	Partial                      bool         `json:"partial"`
	TimeDistributionShiftWarning bool         `json:"time_distribution_shift_warning"`
	// Warnings 计数不守恒等异常提示（正常为空，omitempty 不出现在响应里）。
	Warnings []string `json:"warnings,omitempty"`
	// v1.2 §9.4：导入后若待确认行落在已物化区内，当场整表重算（不留错误余额给下次查询）。
	Rematerialized    bool `json:"rematerialized"`
	RematerializeRows int  `json:"rematerialize_rows"`
	// PendingFrozen 本次导入产生的「待确认冻结」行数（freeze_mode=defer 且早于边界）。
	// 与 FrozenCreated（落在冻结区的新建笔数，统计口径）并列，前端据此展示「待确认 N 行」。
	PendingFrozen int `json:"pending_frozen"`
}

// Commit 提交入库（幂等 + 200 行/事务，§4.6/§4.8）。
func (s *ImportSheetService) Commit(ctx context.Context, in *CommitInput) (*CommitResult, error) {
	if in == nil {
		return nil, apierr.New(apierr.CodeValidation, "请求体不能为空")
	}
	m, err := s.loadManifest(in.BatchID, false)
	if err != nil {
		return nil, err
	}
	accountID, err := s.resolveAccountID(in.Defaults.AccountID)
	if err != nil {
		return nil, err
	}

	// 计划来自 preview 落盘的 manifest
	plans := s.plansForCommit(m, in.Sheets)
	if len(plans) == 0 {
		return nil, apierr.New(apierr.CodeImportSheetInvalid,
			"没有可提交的表：请先调用 preview 完成映射")
	}
	out, err := s.parsePlans(m, plans)
	if err != nil {
		return nil, err
	}
	// 行选择
	allRows := out.rows
	selected := allRows
	if len(in.RowKeys) > 0 {
		want := map[string]bool{}
		for _, k := range in.RowKeys {
			want[k] = true
		}
		selected = make([]*sheetparse.ParsedRow, 0, len(in.RowKeys))
		for _, r := range allRows {
			if want[r.RowKey] {
				selected = append(selected, r)
			}
		}
		if len(selected) == 0 {
			return nil, apierr.New(apierr.CodeValidation, "row_keys 未命中任何可解析行")
		}
	}
	// 提交阶段的分布必须只针对本次选中的行重算（§2.3 time_distribution_shift_warning）
	applyDayDistribution(selected)

	// 时间分布是否相对预览发生漂移
	shiftWarning := false
	previewTimes, err := s.readPreviewTimes(m.BatchID)
	if err != nil {
		return nil, err
	}
	if len(in.RowKeys) > 0 && len(previewTimes) > 0 {
		for _, r := range selected {
			if t, ok := previewTimes[r.RowKey]; ok && t != r.TimeUnix {
				shiftWarning = true
				break
			}
		}
	}

	// 冻结区判定（同一算法：分布后的时间参与判定）
	boundary := s.balance.FreezeBoundary(time.Now().Unix())
	// §9.7 冻结策略：缺省/非法值一律按 defer（用户诉求：先核对再冻结）
	freezeMode := "defer"
	if strings.EqualFold(strings.TrimSpace(in.FreezeMode), "immediate") {
		freezeMode = "immediate"
	}
	frozenCount := 0
	for _, r := range selected {
		if r.TimeUnix < boundary {
			frozenCount++
		}
	}
	if frozenCount > 0 && !in.AllowFrozen {
		return nil, apierr.New(apierr.CodeFrozen,
			"所选行包含冻结区交易，需勾选「允许写入冻结区」后提交").
			WithDetails(map[string]any{
				"frozen_rows":     frozenCount,
				"freeze_boundary": timeutil.FormatLocal(boundary),
				"suggest":         "重新提交并设置 allow_frozen=true",
			})
	}

	// 软重复（提交时重算一次，防止预览后库内新增）
	dupIdx := map[int]ImportDup{}
	softIdx := map[int]*ImportDup{}
	if err = s.dedupAnnotate(selected, accountID, dupIdx, softIdx); err != nil {
		return nil, err
	}

	// 自动创建分类 / 标签（1 个 WriteTx）
	catRes := &CategoryResolution{IDs: map[string]string{}, Created: []CreatedRef{}, Warnings: []string{}}
	tagRes := &TagResolution{IDs: map[string]string{}, Created: []CreatedRef{}, Warnings: []string{}}
	catNeeds := s.categoryNeeds(selected, plans)
	tagNames := s.tagNeeds(selected)
	if err = s.s.WriteTx(ctx, func(tx *sql.Tx) error {
		var e error
		if in.CreateMissingCategories {
			catRes, e = EnsureCategoriesTx(tx, catNeeds)
			if e != nil {
				return e
			}
		} else {
			catRes, e = lookupCategoriesTx(tx, catNeeds)
			if e != nil {
				return e
			}
		}
		if in.CreateMissingTags {
			tagRes, e = EnsureTagsTx(tx, tagNames)
			if e != nil {
				return e
			}
		} else {
			tagRes, e = lookupTagsTx(tx, tagNames)
			if e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	res := &CommitResult{
		BatchID: m.BatchID, CreatedCategories: catRes.Created, CreatedTags: tagRes.Created,
		TransactionIDs: []string{},
	}
	if res.CreatedCategories == nil {
		res.CreatedCategories = []CreatedRef{}
	}
	if res.CreatedTags == nil {
		res.CreatedTags = []CreatedRef{}
	}

	// 组装待写行（跳过重复/软重复/无效）
	type pending struct {
		row *sheetparse.ParsedRow
		in  *CreateTxnInput
	}
	var batch []pending
	for i, r := range selected {
		if dupIdx[i].IsDup {
			// external_id 硬重复（唯一索引）：始终跳过，与 skip_duplicates 取值无关。
			// 内容重复（软重复）走下一分支，由 skip_soft_duplicates 决定。
			res.SkippedDup++
			continue
		}
		if _, isSoft := softIdx[i]; isSoft && in.SkipSoftDuplicates {
			res.SkippedSoftDup++
			continue
		}
		catID, subID := s.resolveCategoryIDs(r, plans, catRes)
		if r.CategoryName != "" && catID == nil && in.CreateMissingCategories {
			// 分类创建失败（理论上不会发生）→ 计数为无效行
			res.SkippedInvalid++
			continue
		}
		tagIDs := s.resolveTagIDs(r, tagRes)
		if len(tagIDs) == 0 {
			tagIDs = in.Defaults.TagIDs
		}
		txTime := r.TimeUnix
		// §9.7：defer（默认）时，早于冻结边界的行标「待确认」=0（可写、不物化）；
		// immediate 等价 v1.2 已交付行为（全部 =1）。
		rowConfirmed := true
		if freezeMode == "defer" && txTime < boundary {
			rowConfirmed = false
		}
		batch = append(batch, pending{row: r, in: &CreateTxnInput{
			TransactionTime: &txTime,
			TransactionType: r.Type,
			AmountCents:     r.AmountCents,
			Currency:        "CNY",
			AccountID:       accountID,
			CategoryID:      catID,
			SubCategoryID:   subID,
			MerchantText:    r.MerchantText,
			Remark:          r.Remark,
			TagIDs:          tagIDs,
			Source:          "import",
			ExternalID:      r.ExternalID,
			AllowFrozen:     true,
			TimePrecision:   r.TimePrecision,
			FreezeConfirmed: &rowConfirmed,
			ImportBatchID:   m.BatchID,
		}})
	}

	// 分块写入：每 importCommitChunk 行 1 个事务（失败重试 1 次，仍失败则中止后续块）
	var minDeferred int64
	for start := 0; start < len(batch); start += importCommitChunk {
		end := start + importCommitChunk
		if end > len(batch) {
			end = len(batch)
		}
		chunk := batch[start:end]
		inputs := make([]*CreateTxnInput, len(chunk))
		for i := range chunk {
			inputs[i] = chunk[i].in
		}
		br, cerr := s.txn.CreateBatch(ctx, inputs, in.SkipDuplicates)
		if cerr != nil {
			br, cerr = s.txn.CreateBatch(ctx, inputs, in.SkipDuplicates)
		}
		if cerr != nil {
			res.Partial = true
			break
		}
		res.Created += len(br.Created)
		res.FrozenCreated += br.FrozenCreated
		for _, t := range br.Created {
			if t.FreezeConfirmed == 0 {
				res.PendingFrozen++
				if minDeferred == 0 || t.TransactionTime < minDeferred {
					minDeferred = t.TransactionTime
				}
			}
		}
		res.SkippedDup += countSkips(br.Skipped, BatchSkipDuplicate)
		res.SkippedInvalid += countSkips(br.Skipped, BatchSkipInvalid)
		if len(br.Created) > 0 && res.Created <= importTXIDLimit {
			for _, t := range br.Created {
				res.TransactionIDs = append(res.TransactionIDs, t.ID)
			}
		}
	}
	if res.Created > importTXIDLimit {
		res.TransactionIDs = []string{}
		res.TransactionIDsOmitted = true
	}

	// 批次状态幂等守卫
	affected, err := s.batches.MarkCommitted(m.BatchID, int64(res.Created),
		int64(res.SkippedDup+res.SkippedSoftDup), int64(res.SkippedInvalid))
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, apierr.New(apierr.CodeImportBatchCommitted, "该批次已提交，请勿重复提交").
			WithDetails(map[string]any{"batch_id": m.BatchID})
	}
	// §9.4：待确认行若能落进「已物化区」（比 last_frozen_at 更早），必须当场整表重算，
	// 否则钳制未生效、这些行的金额会暂时从余额里消失（违反 I3）。
	if last, lerr := s.balance.LastFrozenAt(); lerr == nil && minDeferred > 0 && minDeferred < last {
		if serr := s.meta.Set("frozen_write_flag", "1"); serr != nil {
			slog.Error("置 frozen_write_flag 失败", "err", serr)
		}
		if s.mat != nil {
			if mr, merr := s.mat.ForceFull(ctx, time.Now()); merr != nil {
				slog.Error("导入后整表重算失败（等待下次查询补偿）", "err", merr)
				res.Warnings = append(res.Warnings, "导入后重算失败："+merr.Error())
			} else {
				res.Rematerialized = true
				res.RematerializeRows = mr.RowsProcessed
			}
		}
	}

	flag, _ := s.meta.Get("frozen_write_flag")
	res.RematerializeTriggered = flag == "1"
	res.TimeDistributionShiftWarning = shiftWarning

	// 计数守恒（§4.6 铁律）：每一条被选中的行必须落进 created / skipped_dup /
	// skipped_soft_dup / skipped_invalid 四桶之一，绝不静默丢弃。
	if !res.Partial {
		accounted := res.Created + res.SkippedDup + res.SkippedSoftDup + res.SkippedInvalid
		if accounted != len(selected) {
			msg := fmt.Sprintf("导入计数不守恒：已选 %d 行，落桶合计 %d 行"+
				"（created=%d dup=%d soft_dup=%d invalid=%d）",
				len(selected), accounted, res.Created, res.SkippedDup,
				res.SkippedSoftDup, res.SkippedInvalid)
			slog.Error("导入计数不守恒", "batch_id", m.BatchID,
				"selected", len(selected), "accounted", accounted,
				"created", res.Created, "skipped_dup", res.SkippedDup,
				"skipped_soft_dup", res.SkippedSoftDup, "skipped_invalid", res.SkippedInvalid)
			res.Warnings = append(res.Warnings, msg)
		}
	}
	return res, nil
}

// countSkips 统计指定原因的跳过数。
func countSkips(skips []BatchSkip, reason string) int {
	n := 0
	for _, s := range skips {
		if s.Reason == reason {
			n++
		}
	}
	return n
}

// plansForCommit 从 manifest 取提交计划（按请求顺序，未指定则全部已预览的表）。
func (s *ImportSheetService) plansForCommit(m *Manifest, refs []CommitSheetRef) []*sheetparse.SheetPlan {
	var out []*sheetparse.SheetPlan
	want := map[string]bool{}
	for _, r := range refs {
		want[planKey(r.FileID, r.SheetIndex)] = true
	}
	for i := range m.Files {
		for j := range m.Files[i].Sheets {
			sh := &m.Files[i].Sheets[j]
			if sh.Plan == nil || sh.Plan.Skip {
				continue
			}
			if len(want) > 0 && !want[planKey(m.Files[i].FileID, sh.SheetIndex)] {
				continue
			}
			out = append(out, sh.Plan)
		}
	}
	return out
}

// categoryNeeds 汇总本次提交涉及的全部分类（含 category_split 的两级）。
func (s *ImportSheetService) categoryNeeds(rows []*sheetparse.ParsedRow,
	plans []*sheetparse.SheetPlan) []CategoryNeed {
	splitByKey := map[string]string{}
	for _, p := range plans {
		splitByKey[planKey(p.FileID, p.SheetIndex)] = p.CategorySplit
	}
	needMap := map[string]CategoryNeed{}
	var order []string
	for _, r := range rows {
		if r.CategoryName == "" {
			continue
		}
		sep := splitByKey[planKey(r.FileID, r.SheetIndex)]
		toL1, toL2 := sheetparse.SplitCategory(r.CategoryName, sep)
		if toL1 != "" {
			key := CategoryKey("", toL1)
			if _, ok := needMap[key]; !ok {
				order = append(order, key)
				needMap[key] = CategoryNeed{Name: toL1, TransactionType: r.Type, Level: 1}
			}
		}
		if toL2 != "" {
			key := CategoryKey(toL1, toL2)
			if _, ok := needMap[key]; !ok {
				order = append(order, key)
				needMap[key] = CategoryNeed{Name: toL2, ParentName: toL1,
					TransactionType: r.Type, Level: 2}
			}
		}
	}
	out := make([]CategoryNeed, 0, len(order))
	for _, k := range order {
		out = append(out, needMap[k])
	}
	return out
}

// tagNeeds 汇总本次提交涉及的标签名（去重保序）。
func (s *ImportSheetService) tagNeeds(rows []*sheetparse.ParsedRow) []string {
	var names []string
	for _, r := range rows {
		names = append(names, r.TagNames...)
	}
	return splitTagsSafe(names)
}

// resolveCategoryIDs 行 → (category_id, sub_category_id)。
func (s *ImportSheetService) resolveCategoryIDs(r *sheetparse.ParsedRow,
	plans []*sheetparse.SheetPlan, res *CategoryResolution) (*string, *string) {
	if r.CategoryName == "" {
		return nil, nil
	}
	sep := ""
	for _, p := range plans {
		if p.FileID == r.FileID && p.SheetIndex == r.SheetIndex {
			sep = p.CategorySplit
			break
		}
	}
	l1, l2 := sheetparse.SplitCategory(r.CategoryName, sep)
	var catID, subID *string
	if id, ok := res.IDs[CategoryKey("", l1)]; ok {
		v := id
		catID = &v
	}
	if l2 != "" {
		if id, ok := res.IDs[CategoryKey(l1, l2)]; ok {
			v := id
			subID = &v
		}
	}
	return catID, subID
}

// resolveTagIDs 行标签名 → 标签 ID 列表。
func (s *ImportSheetService) resolveTagIDs(r *sheetparse.ParsedRow, res *TagResolution) []string {
	out := make([]string, 0, len(r.TagNames))
	for _, n := range r.TagNames {
		if id, ok := res.IDs[n]; ok {
			out = append(out, id)
		}
	}
	return out
}

// lookupCategoriesTx 只查询不创建（create_missing_categories=false）。
func lookupCategoriesTx(tx *sql.Tx, needs []CategoryNeed) (*CategoryResolution, error) {
	res := &CategoryResolution{IDs: map[string]string{}, Created: []CreatedRef{}, Warnings: []string{}}
	for _, need := range needs {
		parent := sheetparse.NormalizeName(need.ParentName)
		id, _, found, err := LookupCategory(tx, parent, need.Name)
		if err != nil {
			return nil, err
		}
		if found {
			res.IDs[CategoryKey(parent, need.Name)] = id
		} else {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("分类「%s」不存在且未开启自动创建，该行分类留空", need.Name))
		}
	}
	return res, nil
}

// lookupTagsTx 只查询不创建。
func lookupTagsTx(tx *sql.Tx, names []string) (*TagResolution, error) {
	res := &TagResolution{IDs: map[string]string{}, Created: []CreatedRef{}, Warnings: []string{}}
	for _, n := range names {
		var id string
		err := tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, sheetparse.NormalizeName(n)).Scan(&id)
		if err == nil {
			res.IDs[n] = id
			continue
		}
	}
	return res, nil
}

// Batches 导入批次审计列表（GET /api/import/batches）。
func (s *ImportSheetService) Batches(limit int) ([]store.ImportBatch, error) {
	return s.batches.List(limit)
}
