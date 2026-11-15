package sheetparse

import (
	"encoding/csv"
	"strings"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/csvparse"
)

// csvTable 基于内存记录的表格迭代器（CSV 体积受 30MB 上限约束，可整份读入）。
type csvTable struct {
	records [][]string
	pos     int
}

// Next 返回下一行记录（行号 1 基，与文件行一致）。
func (t *csvTable) Next() ([]string, int, bool, error) {
	if t.pos >= len(t.records) {
		return nil, 0, false, nil
	}
	rec := t.records[t.pos]
	t.pos++
	return trimTrailingEmpty(rec), t.pos, true, nil
}

// Close CSV 无需释放资源。
func (t *csvTable) Close() error { return nil }

// csvDelimiters 分隔符嗅探候选（, ; TAB）。
var csvDelimiters = []rune{',', ';', '\t'}

// decodeAndSniff 解码文本（BOM/UTF-8/GBK）并嗅探分隔符。
func decodeAndSniff(data []byte) (text, encoding string, delim rune) {
	text, encoding = csvparse.DecodeText(data)
	best, bestFields := ',', 1
	for _, d := range csvDelimiters {
		rec, err := readFirstRecord(text, d)
		if err != nil {
			continue
		}
		if len(rec) > bestFields {
			bestFields, best = len(rec), d
		}
	}
	return text, encoding, best
}

// readFirstRecord 用指定分隔符读第一条记录（嗅探用）。
func readFirstRecord(text string, delim rune) ([]string, error) {
	r := newCSVReader(strings.NewReader(text), delim)
	return r.Read()
}

// newCSVReader 构造宽松 CSV reader（字段数不一致、引号不规范都容忍）。
func newCSVReader(r *strings.Reader, delim rune) *csv.Reader {
	cr := csv.NewReader(r)
	cr.Comma = delim
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true
	return cr
}

// parseCSV 解析整份 CSV 文本。
func parseCSV(text string, delim rune) ([][]string, error) {
	cr := newCSVReader(strings.NewReader(text), delim)
	records, err := cr.ReadAll()
	if err != nil {
		return nil, apierr.Newf(apierr.CodeUnsupportedSpreadsheet,
			"CSV 解析失败（请确认是逗号/分号/制表符分隔的文本表格）: %v", err)
	}
	return records, nil
}

// OpenCSVTable 打开 CSV（sheet_index 仅支持 0）。
func OpenCSVTable(data []byte, sheetIndex int) (Table, error) {
	if sheetIndex != 0 {
		return nil, apierr.New(apierr.CodeImportSheetInvalid, "CSV 文件只有一张表（sheet_index 必须为 0）")
	}
	text, _, delim := decodeAndSniff(data)
	records, err := parseCSV(text, delim)
	if err != nil {
		return nil, err
	}
	return &csvTable{records: records}, nil
}

// ProbeCSV 探测 CSV：整份作为一个工作表，表名取文件名。
func ProbeCSV(data []byte, fileName string, maxSample int) (*FileMeta, error) {
	text, encoding, delim := decodeAndSniff(data)
	records, err := parseCSV(text, delim)
	if err != nil {
		return nil, err
	}
	if len(records) > MaxSheetRows {
		return nil, apierr.Newf(apierr.CodeImportTooLarge,
			"CSV 共 %d 行，超过单表 %d 行上限，请拆分文件后重试", len(records), MaxSheetRows)
	}
	maxCols := 0
	trimmed := make([][]string, len(records))
	for i, rec := range records {
		trimmed[i] = trimTrailingEmpty(rec)
		if len(trimmed[i]) > maxCols {
			maxCols = len(trimmed[i])
		}
	}
	name := strings.TrimSuffix(fileName, ".csv")
	if name == "" {
		name = "CSV"
	}
	m := &FileMeta{Kind: KindCSV, Encoding: encoding, Sheets: []SheetMeta{}}
	m.Sheets = append(m.Sheets, buildSheetMeta(0, name, trimmed, len(trimmed), maxCols, maxSample))
	return m, nil
}
