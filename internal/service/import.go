package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/csvparse"
	"ledger/internal/pkg/ids"
	"ledger/internal/pkg/timeutil"
	"ledger/internal/store"
)

// ImportService CSV 两阶段导入（§5.7 端点 54–55、§6.9）。
type ImportService struct {
	s       *store.Store
	meta    *store.MetaRepo
	master  *store.MasterRepo
	txn     *TxnService
	balance *BalanceService
	dataDir string
}

// NewImportService 构造。
func NewImportService(s *store.Store, meta *store.MetaRepo, master *store.MasterRepo,
	txn *TxnService, balance *BalanceService, dataDir string) *ImportService {
	return &ImportService{s: s, meta: meta, master: master, txn: txn, balance: balance, dataDir: dataDir}
}

// ImportDup 去重信息。
type ImportDup struct {
	IsDup                bool    `json:"is_dup"`
	Reason               string  `json:"reason,omitempty"`
	MatchedTransactionID *string `json:"matched_transaction_id,omitempty"`
}

// ImportPreviewRow 预览行。
type ImportPreviewRow struct {
	RowNo       int               `json:"row_no"`
	Raw         map[string]string `json:"raw"`
	Parsed      *csvparse.Parsed  `json:"parsed"`
	Dup         ImportDup         `json:"dup"`
	Frozen      bool              `json:"frozen"`
	Warnings    []string          `json:"warnings"`
	NeedConfirm []string          `json:"need_confirm"`
	SkipReason  string            `json:"skip_reason,omitempty"`
}

// ImportSummary 预览汇总。
type ImportSummary struct {
	TotalRows   int `json:"total_rows"`
	ParsedOK    int `json:"parsed_ok"`
	Skipped     int `json:"skipped"`
	DupCount    int `json:"dup_count"`
	FrozenCount int `json:"frozen_count"`
}

// ImportPreview /import/csv/preview 响应。
type ImportPreview struct {
	BatchID         string             `json:"batch_id"`
	Source          string             `json:"source"`
	Encoding        string             `json:"encoding"`
	Columns         []string           `json:"columns"`
	Mapping         map[string]string  `json:"mapping"`
	Summary         ImportSummary      `json:"summary"`
	Rows            []ImportPreviewRow `json:"rows"`
	SuggestDefaults map[string]any     `json:"suggest_defaults"`
}

// importBatch 落盘批次（commit 时读取）。
type importBatch struct {
	BatchID   string             `json:"batch_id"`
	Source    string             `json:"source"`
	Encoding  string             `json:"encoding"`
	CreatedAt int64              `json:"created_at"`
	Columns   []string           `json:"columns"`
	Mapping   map[string]string  `json:"mapping"`
	Rows      []ImportPreviewRow `json:"rows"`
}

func (s *ImportService) batchPath(batchID string) string {
	return filepath.Join(s.dataDir, "tmp", "import", batchID+".json")
}

// Preview 解析账单并落盘批次（§5.7 端点 54）。
func (s *ImportService) Preview(ctx context.Context, data []byte, accountID, sourceHint string) (*ImportPreview, error) {
	f, err := csvparse.Parse(data)
	if err != nil {
		return nil, err
	}
	if sourceHint != "" && sourceHint != "auto" && sourceHint != f.Source {
		return nil, apierr.New(apierr.CodeUnsupportedCSV, "账单格式与指定来源不符（实际识别为 "+f.Source+"）").
			WithDetails(map[string]any{"detected_headers": f.Columns})
	}
	if accountID == "" {
		accountID, _ = s.meta.Get("default_account_id")
	}
	boundary := s.balance.FreezeBoundary(time.Now().Unix())

	out := &ImportPreview{
		BatchID: ids.ShortID("imp_"), Source: f.Source, Encoding: f.Encoding,
		Columns: f.Columns, Mapping: f.Mapping, Rows: []ImportPreviewRow{},
		SuggestDefaults: map[string]any{},
	}
	for _, row := range f.Rows {
		pr := ImportPreviewRow{
			RowNo: row.RowNo, Raw: row.Raw, Parsed: row.Parsed,
			Warnings: append([]string{}, row.Warnings...), SkipReason: row.SkipReason,
			NeedConfirm: []string{},
		}
		out.Summary.TotalRows++
		if row.Parsed == nil {
			out.Summary.Skipped++
			out.Rows = append(out.Rows, pr)
			continue
		}
		out.Summary.ParsedOK++
		// 冻结标记（中文 warning）
		if row.Parsed.TimeUnix < boundary {
			pr.Frozen = true
			pr.Warnings = append(pr.Warnings, "交易时间落在冻结区，确认后不可直接修改")
			out.Summary.FrozenCount++
		}
		// 行指纹（无 external_id 时）→ 幂等重导
		if row.Parsed.ExternalID == "" && accountID != "" {
			row.Parsed.ExternalID = csvparse.Fingerprint(
				timeutil.LocalDate(row.Parsed.TimeUnix), row.Parsed.AmountCents,
				accountID, row.Parsed.MerchantText, row.Parsed.Type)
		}
		// 去重：external_id 命中
		if row.Parsed.ExternalID != "" {
			if matched, err := s.findByExternalID(row.Parsed.ExternalID); err != nil {
				return nil, err
			} else if matched != nil {
				pr.Dup = ImportDup{IsDup: true, Reason: "external_id", MatchedTransactionID: &matched.ID}
				out.Summary.DupCount++
			}
		}
		// 分类需人工确认（基础版不做自动分类）
		pr.NeedConfirm = append(pr.NeedConfirm, "category_id")
		out.Rows = append(out.Rows, pr)
	}

	defCtx, _ := s.meta.Get("default_category_expense_id")
	out.SuggestDefaults = map[string]any{
		"account_id":  nilIfEmpty(accountID),
		"category_id": nilIfEmpty(defCtx),
	}
	// 落盘
	batch := importBatch{BatchID: out.BatchID, Source: out.Source, Encoding: out.Encoding,
		CreatedAt: timeutil.NowUnix(), Columns: out.Columns, Mapping: out.Mapping, Rows: out.Rows}
	buf, err := json.Marshal(batch)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(s.batchPath(out.BatchID)), 0o755); err != nil {
		return nil, err
	}
	if err = os.WriteFile(s.batchPath(out.BatchID), buf, 0o644); err != nil {
		return nil, err
	}
	return out, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// findByExternalID 查已存在交易。
func (s *ImportService) findByExternalID(externalID string) (*store.Transaction, error) {
	return s.s.FindTxByExternalID(externalID)
}

// ImportCommitDefaults commit 默认值。
type ImportCommitDefaults struct {
	AccountID      string   `json:"account_id"`
	CategoryID     string   `json:"category_id"`
	SubCategoryID  *string  `json:"sub_category_id"`
	TagIDs         []string `json:"tag_ids"`
	MerchantType   string   `json:"merchant_type"`
}

// ImportOverride 单行覆盖。
type ImportOverride struct {
	RowNo           int     `json:"row_no"`
	TransactionType *string `json:"transaction_type"`
	AmountCents     *int64  `json:"amount_cents"`
	CategoryID      *string `json:"category_id"`
	SubCategoryID   *string `json:"sub_category_id"`
	MerchantID      *string `json:"merchant_id"`
	Remark          *string `json:"remark"`
}

// ImportCommitInput commit 请求。
type ImportCommitInput struct {
	BatchID        string               `json:"batch_id"`
	RowNos         []int                `json:"row_nos"`
	Defaults       ImportCommitDefaults `json:"defaults"`
	Overrides      []ImportOverride     `json:"overrides"`
	SkipDuplicates bool                 `json:"skip_duplicates"`
	AllowFrozen    bool                 `json:"allow_frozen"`
}

// ImportCommitResult commit 响应。
type ImportCommitResult struct {
	Created               int      `json:"created"`
	SkippedDup            int      `json:"skipped_dup"`
	SkippedInvalid        int      `json:"skipped_invalid"`
	FrozenCreated         int      `json:"frozen_created"`
	RematerializeTriggered bool    `json:"rematerialize_triggered"`
	TransactionIDs        []string `json:"transaction_ids"`
	BatchID               string   `json:"batch_id"`
}

// Commit 提交批次（§5.7 端点 55）。
func (s *ImportService) Commit(ctx context.Context, in *ImportCommitInput) (*ImportCommitResult, error) {
	if in.BatchID == "" {
		return nil, apierr.New(apierr.CodeValidation, "batch_id 必填")
	}
	raw, err := os.ReadFile(s.batchPath(in.BatchID))
	if err != nil {
		return nil, apierr.New(apierr.CodeImportBatchExpired, "导入批次不存在或已过期，请重新上传")
	}
	var batch importBatch
	if err = json.Unmarshal(raw, &batch); err != nil {
		return nil, apierr.New(apierr.CodeImportBatchExpired, "导入批次文件损坏，请重新上传")
	}
	if timeutil.NowUnix()-batch.CreatedAt > 24*3600 {
		return nil, apierr.New(apierr.CodeImportBatchExpired, "导入批次已超过 24 小时，请重新上传")
	}
	accountID := in.Defaults.AccountID
	if accountID == "" {
		accountID, _ = s.meta.Get("default_account_id")
	}
	if accountID == "" {
		return nil, apierr.New(apierr.CodeValidation, "defaults.account_id 必填")
	}
	if _, err = s.master.GetAccount(accountID); err != nil {
		return nil, apierr.New(apierr.CodeValidation, "defaults.account_id 不存在")
	}

	// 选中行
	selected := map[int]bool{}
	for _, n := range in.RowNos {
		selected[n] = true
	}
	overrides := map[int]ImportOverride{}
	for _, ov := range in.Overrides {
		overrides[ov.RowNo] = ov
	}
	var todo []ImportPreviewRow
	for _, row := range batch.Rows {
		if row.Parsed == nil {
			continue
		}
		if len(selected) > 0 && !selected[row.RowNo] {
			continue
		}
		todo = append(todo, row)
	}
	if len(todo) == 0 {
		return nil, apierr.New(apierr.CodeValidation, "没有可提交的行")
	}

	// 冻结区整体拒绝（不做部分成功）
	if !in.AllowFrozen {
		for _, row := range todo {
			if row.Frozen && !overrideClearsFrozen(row, overrides[row.RowNo]) {
				return nil, apierr.New(apierr.CodeFrozen, "所选行包含冻结区交易，需勾选「允许写入冻结区」后提交").
					WithDetails(map[string]any{"row_no": row.RowNo,
						"transaction_time": row.Parsed.TimeStr,
						"freeze_boundary":  timeutil.FormatLocal(s.balance.FreezeBoundary(time.Now().Unix())),
						"suggest":          "重新勾选 allow_frozen=true"})
			}
		}
	}

	res := &ImportCommitResult{TransactionIDs: []string{}, BatchID: in.BatchID}
	for _, row := range todo {
		p := *row.Parsed
		ov, hasOv := overrides[row.RowNo]
		if hasOv {
			if ov.TransactionType != nil {
				p.Type = *ov.TransactionType
			}
			if ov.AmountCents != nil {
				p.AmountCents = *ov.AmountCents
			}
			if ov.Remark != nil {
				p.Remark = *ov.Remark
			}
		}
		if p.Type != "expense" && p.Type != "income" {
			res.SkippedInvalid++
			continue
		}
		if p.AmountCents <= 0 {
			res.SkippedInvalid++
			continue
		}
		// 去重（commit 时再判一次，防止预览后库内新增）
		if p.ExternalID != "" {
			if matched, err := s.findByExternalID(p.ExternalID); err != nil {
				return nil, err
			} else if matched != nil || row.Dup.IsDup {
				if in.SkipDuplicates {
					res.SkippedDup++
					continue
				}
			}
		}

		catID := in.Defaults.CategoryID
		if catID == "" {
			if p.Type == "income" {
				catID, _ = s.meta.Get("default_category_income_id")
			} else {
				catID, _ = s.meta.Get("default_category_expense_id")
			}
		}
		var catPtr *string
		if hasOv && ov.CategoryID != nil {
			catPtr = ov.CategoryID
		} else if catID != "" {
			catPtr = &catID
		}
		var subPtr *string
		if hasOv && ov.SubCategoryID != nil {
			subPtr = ov.SubCategoryID
		} else if in.Defaults.SubCategoryID != nil {
			subPtr = in.Defaults.SubCategoryID
		}
		var mchPtr *string
		if hasOv && ov.MerchantID != nil {
			mchPtr = ov.MerchantID
		}
		txTime := p.TimeUnix
		createRes, err := s.txn.Create(ctx, &CreateTxnInput{
			TransactionTime:  &txTime,
			TransactionType:  p.Type,
			AmountCents:      p.AmountCents,
			Currency:         "CNY",
			AccountID:        accountID,
			CategoryID:       catPtr,
			SubCategoryID:    subPtr,
			MerchantID:       mchPtr,
			MerchantText:     p.MerchantText,
			CounterpartyName: p.CounterpartyName,
			Remark:           p.Remark,
			TagIDs:           in.Defaults.TagIDs,
			Source:           "csv",
			ExternalID:       p.ExternalID,
			AllowFrozen:      true,                              // 已在整体校验中放行
			AllowDuplicate:   !in.SkipDuplicates,                // 不跳过重复时强插并带 warning
		})
		if err != nil {
			if ae, ok := err.(*apierr.Error); ok && ae.Code == apierr.CodeDuplicate {
				res.SkippedDup++
				continue
			}
			var ae *apierr.Error
			if errors.As(err, &ae) && ae.Code == apierr.CodeValidation {
				res.SkippedInvalid++
				continue
			}
			return nil, err
		}
		res.Created++
		res.TransactionIDs = append(res.TransactionIDs, createRes.Primary.ID)
		if row.Frozen {
			res.FrozenCreated++
		}
	}
	flag, _ := s.meta.Get("frozen_write_flag")
	res.RematerializeTriggered = flag == "1"
	return res, nil
}

// overrideClearsFrozen 覆盖后的交易时间是否已移出冻结区（暂不支持改时间，恒 false）。
func overrideClearsFrozen(row ImportPreviewRow, ov ImportOverride) bool {
	_ = row
	_ = ov
	return false
}

// String 调试。
func (r *ImportCommitResult) String() string {
	return fmt.Sprintf("created=%d dup=%d invalid=%d frozen=%d", r.Created, r.SkippedDup, r.SkippedInvalid, r.FrozenCreated)
}
