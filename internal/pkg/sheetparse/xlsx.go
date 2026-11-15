package sheetparse

import (
	"bytes"
	"fmt"
	"io"

	"github.com/xuri/excelize/v2"

	"ledger/internal/pkg/apierr"
)

// Table 流式行迭代器（xlsx / csv 共用）。
// Next 返回当前行（尾部空单元格已裁掉）、行号（1 基，含表头）与是否还有数据。
type Table interface {
	Next() (row []string, rowNo int, ok bool, err error)
	Close() error
}

// xlsxTable 基于 excelize.Rows 的流式读取（禁止 GetRows 全量取，§1.3）。
type xlsxTable struct {
	f    *excelize.File
	rows *excelize.Rows
	seq  int
}

// Next 读取下一行原始值（RawCellValue=true，日期列拿到序列号）。
func (t *xlsxTable) Next() ([]string, int, bool, error) {
	if !t.rows.Next() {
		return nil, 0, false, t.rows.Error()
	}
	cols, err := t.rows.Columns(excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, 0, false, err
	}
	t.seq++
	return trimTrailingEmpty(cols), t.seq, true, nil
}

// Close 释放 excelize 临时文件。
func (t *xlsxTable) Close() error {
	err := t.rows.Close()
	if cerr := t.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// openXLSX 打开工作簿（内存中的字节流）。
func openXLSX(data []byte) (*excelize.File, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, apierr.New(apierr.CodeUnsupportedSpreadsheet,
			"无法读取该表格文件（可能已加密或已损坏）。请用 Excel/WPS 另存为 .xlsx 或 .csv 后重试").
			WithDetails(map[string]any{"reason": err.Error()})
	}
	return f, nil
}

// OpenXLSXTable 打开指定工作表（sheet_index 为 GetSheetList 顺序）。
func OpenXLSXTable(data []byte, sheetIndex int) (Table, error) {
	f, err := openXLSX(data)
	if err != nil {
		return nil, err
	}
	names := f.GetSheetList()
	if sheetIndex < 0 || sheetIndex >= len(names) {
		_ = f.Close()
		return nil, apierr.New(apierr.CodeImportSheetInvalid,
			fmt.Sprintf("sheet_index 越界（有效范围 0..%d）", len(names)-1))
	}
	rows, err := f.Rows(names[sheetIndex])
	if err != nil {
		_ = f.Close()
		return nil, apierr.Newf(apierr.CodeImportSheetInvalid, "无法打开工作表「%s」: %v", names[sheetIndex], err)
	}
	return &xlsxTable{f: f, rows: rows}, nil
}

// scanXLSXSheet 流式扫描一张表：保留前 keep 行、返回总行数与最大列数。
func scanXLSXSheet(f *excelize.File, name string, keep int) (rows [][]string, total, maxCols int, err error) {
	it, err := f.Rows(name)
	if err != nil {
		return nil, 0, 0, err
	}
	kept := make([][]string, 0, keep)
	for it.Next() {
		if err = it.Error(); err != nil {
			break
		}
		cols, cerr := it.Columns(excelize.Options{RawCellValue: true})
		if cerr != nil {
			err = cerr
			break
		}
		total++
		cols = trimTrailingEmpty(cols)
		if len(cols) > maxCols {
			maxCols = len(cols)
		}
		if len(kept) < keep {
			kept = append(kept, cols)
		}
	}
	if cerr := it.Close(); err == nil {
		err = cerr
	}
	return kept, total, maxCols, err
}

// ProbeXLSX 探测工作簿：列出全部工作表并给出表头/映射建议。
func ProbeXLSX(data []byte, maxSample int) (*FileMeta, error) {
	f, err := openXLSX(data)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	date1904 := false
	if props, perr := f.GetWorkbookProps(); perr == nil && props.Date1904 != nil {
		date1904 = *props.Date1904
	}
	m := &FileMeta{Kind: KindXLSX, Date1904: date1904, Sheets: []SheetMeta{}}
	names := f.GetSheetList()
	if len(names) == 0 {
		return nil, apierr.New(apierr.CodeUnsupportedSpreadsheet, "该工作簿没有任何工作表")
	}
	for idx, name := range names {
		kept, total, maxCols, err := scanXLSXSheet(f, name, scanHeaderRows+scanValueRows+maxSample)
		if err != nil {
			return nil, fmt.Errorf("读取工作表「%s」失败: %w", name, err)
		}
		if total > MaxSheetRows {
			return nil, apierr.Newf(apierr.CodeImportTooLarge,
				"工作表「%s」共 %d 行，超过单表 %d 行上限，请拆分文件后重试", name, total, MaxSheetRows)
		}
		m.Sheets = append(m.Sheets, buildSheetMeta(idx, name, kept, total, maxCols, maxSample))
	}
	return m, nil
}

// buildSheetMeta 由扫描结果构造 SheetMeta（xlsx / csv 共用）。
func buildSheetMeta(index int, name string, rows [][]string, total, maxCols, maxSample int) SheetMeta {
	d := Detect(rows, name)
	sm := SheetMeta{
		SheetIndex: index, SheetName: name, RowCount: total, ColCount: maxCols,
		HeaderRow: d.HeaderRow, HeaderRowCandidates: d.Candidates, Columns: d.Columns,
		SampleRows: [][]string{}, SuggestedMapping: d.Mapping,
		SuggestedDirection: d.Direction, SuggestedSkip: d.Skip, SkipReason: d.SkipReason,
		DateHasTime: d.DateHasTime, Warning: d.Warning,
	}
	if sm.HeaderRowCandidates == nil {
		sm.HeaderRowCandidates = []int{}
	}
	if sm.Columns == nil {
		sm.Columns = []string{}
	}
	if sm.SuggestedMapping == nil {
		sm.SuggestedMapping = map[string]string{}
	}
	// 样本行：表头之后 maxSample 行
	start := d.HeaderRow
	for i := start; i < len(rows) && len(sm.SampleRows) < maxSample; i++ {
		sm.SampleRows = append(sm.SampleRows, rows[i])
	}
	return sm
}

// trimTrailingEmpty 去掉行尾空单元格（内部空列保留，保证列索引与表头对齐）。
func trimTrailingEmpty(row []string) []string {
	last := -1
	for i, c := range row {
		if c != "" {
			last = i
		}
	}
	if last < 0 {
		return []string{}
	}
	return row[:last+1]
}

// readAll 便于测试：把 Table 读完。
func readAll(t Table) ([][]string, error) {
	var out [][]string
	for {
		row, _, ok, err := t.Next()
		if err != nil && err != io.EOF {
			return out, err
		}
		if !ok {
			return out, nil
		}
		out = append(out, row)
	}
}
