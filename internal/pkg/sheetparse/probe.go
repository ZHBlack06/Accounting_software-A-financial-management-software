package sheetparse

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"ledger/internal/pkg/apierr"
	"ledger/internal/pkg/csvparse"
)

// 上传/解析硬上限（§1.3）。
const (
	// MaxFileSize 单文件 30MB。
	MaxFileSize = 30 << 20
	// MaxBatchSize 单批合计 60MB。
	MaxBatchSize = 60 << 20
	// MaxSheetRows 单 sheet 20 万行。
	MaxSheetRows = 200000
	// MaxBatchRows 单批解析行数 5 万。
	MaxBatchRows = 50000
)

// 文件魔数。
var (
	zipMagic  = []byte{0x50, 0x4B, 0x03, 0x04}                         // PK\x03\x04
	ole2Magic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1} // BIFF8 / 旧 Office
)

// ErrXLSUnsupported 统一的中文提示（.xls 不支持，§1.2 决策）。
func ErrXLSUnsupported() *apierr.Error {
	return apierr.New(apierr.CodeUnsupportedSpreadsheet,
		"不支持 .xls（Excel 97-2003 二进制格式）。请用 Excel/WPS 打开后「另存为」.xlsx 或 .csv 再导入")
}

// ProbeFile 探测单个上传文件：按扩展名与魔数选择 xlsx / csv 解析器。
// maxSample 为每张表返回的样本行数（通常 8）。
func ProbeFile(data []byte, fileName string, maxSample int) (*FileMeta, error) {
	if len(data) == 0 {
		return nil, apierr.New(apierr.CodeValidation, "文件内容为空")
	}
	if len(data) > MaxFileSize {
		return nil, apierr.Newf(apierr.CodeImportTooLarge,
			"单个文件 %d 字节，超过 %d 字节上限，请拆分后重试", len(data), MaxFileSize)
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == ".xls" {
		return nil, ErrXLSUnsupported()
	}
	if bytes.HasPrefix(data, ole2Magic) {
		return nil, ErrXLSUnsupported()
	}

	var (
		meta *FileMeta
		err  error
	)
	switch {
	case bytes.HasPrefix(data, zipMagic):
		meta, err = ProbeXLSX(data, maxSample)
	case ext == ".csv":
		meta, err = ProbeCSV(data, fileName, maxSample)
	default:
		// 未知扩展名：能当文本解析就当 CSV（.txt 表格很常见），否则明确报错
		if looksLikeText(data) {
			meta, err = ProbeCSV(data, fileName, maxSample)
		} else {
			return nil, apierr.New(apierr.CodeUnsupportedSpreadsheet,
				"无法识别的文件类型，仅支持 .xlsx / .xlsm / .csv")
		}
	}
	if err != nil {
		return nil, err
	}
	meta.FileName = fileName
	meta.Size = int64(len(data))
	meta.SHA1 = SHA1Hex(data)
	return meta, nil
}

// OpenTable 按文件类型打开指定工作表。
func OpenTable(data []byte, kind string, sheetIndex int) (Table, error) {
	if kind == KindCSV {
		return OpenCSVTable(data, sheetIndex)
	}
	return OpenXLSXTable(data, sheetIndex)
}

// SHA1Hex 计算文件 SHA1（去重指纹前缀使用）。
func SHA1Hex(data []byte) string {
	sum := sha1.Sum(data)
	return hex.EncodeToString(sum[:])
}

// RowExternalID 生成导入行去重键：imp:hex(sha1(fileSHA1|sheetIndex|rowNo))[:20]（§4.6）。
func RowExternalID(fileSHA1 string, sheetIndex, rowNo int) string {
	h := sha1.Sum([]byte(fileSHA1 + "|" + strconv.Itoa(sheetIndex) + "|" + strconv.Itoa(rowNo)))
	return "imp:" + hex.EncodeToString(h[:])[:20]
}

// looksLikeText 判断是否像文本表格（UTF-8 合法或可 GBK 解码且含分隔符）。
func looksLikeText(data []byte) bool {
	head := data
	if len(head) > 4096 {
		head = head[:4096]
	}
	if utf8.Valid(head) {
		return strings.ContainsAny(string(head), ",\t;")
	}
	// GBK：解码成功即视为文本表格（二进制文件解码后通常不含分隔符）
	text, _ := csvparse.DecodeText(head)
	return strings.ContainsAny(text, ",\t;")
}
