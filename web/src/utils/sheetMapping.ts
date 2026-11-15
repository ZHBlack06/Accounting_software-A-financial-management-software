/**
 * 表格导入前端纯逻辑（ARCHITECTURE-V12 §5.1–§5.3）：
 * 文件校验、映射字段定义、映射必填校验、按列名批量套用映射。
 * 抽成纯函数以便单测（不依赖 Vue / DOM）。
 */
import type { SheetMapping, TimePrecision } from '@/api/types'

/** 单文件体积上限（§5.1 步 1：单文件 ≤30MB）。 */
export const MAX_FILE_BYTES = 30 * 1024 * 1024

/** `.xls` 拦截提示（§2.2：后端 422 UNSUPPORTED_SPREADSHEET，前端提前拦）。 */
export const XLS_HINT = '不支持 .xls（旧版 Excel 二进制格式），请另存为 .xlsx 或 .csv 后再导入'

/** 允许扩展名（accept 属性 + 校验同源）。 */
export const ACCEPT_EXT = '.xlsx,.xlsm,.csv'

export type MappingFieldKey = keyof SheetMapping

export interface MappingFieldMeta {
  key: MappingFieldKey
  label: string
  /** 必填项：缺失时禁用「下一步」。 */
  required: boolean
  /** 展示在状态列里的用途说明。 */
  hint: string
}

/** 映射目标字段（顺序即 UI 顺序，§5.3 表格）。 */
export const MAPPING_FIELDS: MappingFieldMeta[] = [
  { key: 'date', label: '日期', required: true, hint: '日期列（序列号 / 文本均可）' },
  { key: 'amount', label: '金额', required: true, hint: '金额列（纯数值 / 带符号 / 带币种）' },
  { key: 'direction', label: '收支方向', required: false, hint: '缺失时使用「按表默认方向」' },
  { key: 'category', label: '分类', required: false, hint: '未命中的分类会在提交时自动新建' },
  { key: 'sub_category', label: '二级分类', required: false, hint: '仅当源数据明确两列时映射' },
  { key: 'account', label: '账户', required: false, hint: '缺失时使用默认账户' },
  { key: 'tag', label: '标签', required: false, hint: '支持多值分隔（逗号 / 顿号）' },
  { key: 'remark', label: '备注', required: false, hint: '与「商户」同时存在时各取一列' },
  { key: 'merchant', label: '商户', required: false, hint: '只有一个时优先作为商户' },
  { key: 'external_id', label: '源流水号', required: false, hint: '缺失时按内容指纹去重' },
  { key: 'month', label: '年份月', required: false, hint: '只有「2018年10月」这类列时使用，按当月 1 日处理' },
]

/** 空映射：全部字段为 null（=不导入）。 */
export function emptyMapping(): SheetMapping {
  return {
    date: null,
    month: null,
    amount: null,
    direction: null,
    category: null,
    sub_category: null,
    account: null,
    tag: null,
    remark: null,
    merchant: null,
    external_id: null,
  }
}

/** 把后端 suggested_mapping（可能缺字段）补齐成完整映射。 */
export function normalizeMapping(raw?: Partial<SheetMapping> | null): SheetMapping {
  const base = emptyMapping()
  if (!raw) return base
  for (const f of MAPPING_FIELDS) {
    const v = raw[f.key]
    base[f.key] = typeof v === 'string' && v.trim() !== '' ? v : null
  }
  return base
}

export interface MappingValidationOptions {
  /** 表单里选的「按表默认方向」，用于提示文案。 */
  defaultDirection?: 'expense' | 'income'
}

export interface MappingValidation {
  /** 缺失的必填字段 label（用于红字提示）。 */
  missing: string[]
  /** 致命错误（应禁用「下一步」）。 */
  errors: string[]
  /** 非致命提醒（黄字）。 */
  warnings: string[]
}

/**
 * 映射必填校验（§5.1 步 3 / T06 验收 ②）：
 * - `date` 与 `month` 至少一个（二者皆缺 → 报错）；
 * - `amount` 必填；
 * - 同一列不允许被两个字段占用（UI 里已置灰，这里兜底）。
 */
export function validateMapping(
  mapping: SheetMapping,
  options: MappingValidationOptions = {},
): MappingValidation {
  const missing: string[] = []
  const errors: string[] = []
  const warnings: string[] = []

  const hasDate = Boolean(mapping.date)
  const hasMonth = Boolean(mapping.month)
  if (!hasDate && !hasMonth) {
    missing.push('日期')
    errors.push('请映射「日期」或「年份月」列')
  }
  if (!mapping.amount) {
    missing.push('金额')
    errors.push('请映射「金额」列')
  }

  // 同一来源列被重复映射 → 后端会 400 IMPORT_SHEET_INVALID，这里提前拦。
  const used = new Map<string, string>()
  for (const f of MAPPING_FIELDS) {
    const col = mapping[f.key]
    if (!col) continue
    const prev = used.get(col)
    if (prev) {
      errors.push(`列「${col}」同时被映射到「${prev}」与「${f.label}」`)
    } else {
      used.set(col, f.label)
    }
  }

  if (hasMonth && !hasDate) {
    warnings.push('仅有「年份月」列，将按当月 1 日处理')
  }
  if (!mapping.direction) {
    const d = options.defaultDirection ?? 'expense'
    warnings.push(`未映射收支方向列，全部按「${d === 'income' ? '收入' : '支出'}」导入`)
  }

  return { missing, errors, warnings }
}

/** 「应用到其他表」：按列名相同批量套用（§5.3），返回新映射。 */
export function applyMappingByColumnName(
  source: SheetMapping,
  targetColumns: string[],
): { mapping: SheetMapping; applied: MappingFieldKey[] } {
  const next = emptyMapping()
  const applied: MappingFieldKey[] = []
  const cols = new Set(targetColumns)
  for (const f of MAPPING_FIELDS) {
    const col = source[f.key]
    if (col && cols.has(col)) {
      next[f.key] = col
      applied.push(f.key)
    }
  }
  return { mapping: next, applied }
}

export interface FileCheckResult {
  ok: boolean
  /** 阻止上传的原因（.xls / 体积超限 / 类型不符 / 重复）。 */
  reason: string
}

/**
 * 单文件校验（步 1）：扩展名白名单 + 体积上限。
 * `.xls` 给出「请另存为 .xlsx 或 .csv」的明确指引。
 */
export function checkSheetFile(fileName: string, sizeBytes: number): FileCheckResult {
  const lower = fileName.toLowerCase()
  if (lower.endsWith('.xls')) {
    return { ok: false, reason: `${fileName}：${XLS_HINT}` }
  }
  const allowed = ['.xlsx', '.xlsm', '.csv']
  if (!allowed.some((ext) => lower.endsWith(ext))) {
    return { ok: false, reason: `${fileName}：仅支持 ${ACCEPT_EXT} 格式` }
  }
  if (sizeBytes > MAX_FILE_BYTES) {
    return { ok: false, reason: `${fileName}：文件超过 30MB（${formatSize(sizeBytes)}），请拆分后再导入` }
  }
  return { ok: true, reason: '' }
}

/** 人类可读体积（KB / MB）。 */
export function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 KB'
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`
}

/** 选定表单的提交精度：源数据无时间 → 一律 'day'（§4.1 同日时间分布）。 */
export function pickPrecision(dateHasTime: boolean, globalPrecision: TimePrecision): TimePrecision {
  return dateHasTime ? globalPrecision : 'day'
}
