/**
 * 表格导入向导的共享状态与编排逻辑（ARCHITECTURE-V12 §5.1–§5.3）。
 * 纯 TS（不依赖 DOM），便于在组件间共享并保持单文件体积可控。
 */
import type {
  FreezeMode,
  SheetCommitReq,
  SheetCommitResult,
  SheetPreviewDTO,
  SheetPreviewReq,
  SheetProbeDTO,
  SheetMapping,
  DirectionMode,
  TimePrecision,
} from '@/api/types'
import { normalizeMapping, pickPrecision } from '@/utils/sheetMapping'
import { DEFAULT_FREEZE_MODE } from '@/utils/freeze'

/** 步 2 的候选表单（由 probe 结果展开）。 */
export interface SheetCandidate {
  fileId: string
  fileName: string
  fileNameSize: number
  sheetIndex: number
  sheetName: string
  rowCount: number
  colCount: number
  columns: string[]
  headerRow: number
  headerRowCandidates: number[]
  sampleRows: string[][]
  dateHasTime: boolean
  suggestedSkip: boolean
  skipReason: string
  suggestedMapping: SheetMapping
  suggestedDirection: 'expense' | 'income'
  /** 是否勾选导入（汇总表默认 false）。 */
  selected: boolean
}

/** 步 3 的单表映射配置。 */
export interface SheetConfig {
  key: string
  fileId: string
  fileName: string
  sheetIndex: number
  sheetName: string
  rowCount: number
  columns: string[]
  headerRow: number
  headerRowCandidates: number[]
  mapping: SheetMapping
  suggestedMapping: SheetMapping
  directionMode: DirectionMode
  directionDefault: 'expense' | 'income'
  timePrecision: TimePrecision
  dateHasTime: boolean
}

/** 提交选项（§5.1 步 4）。 */
export interface CommitOptions {
  createMissingCategories: boolean
  createMissingTags: boolean
  skipDuplicates: boolean
  skipSoftDuplicates: boolean
  allowFrozen: boolean
}

/** 向导整体状态：由 SheetImportWizard 持有，子步骤直接读写该对象。 */
export interface WizardState {
  /** 0..4（对应 el-steps 的 5 步）。 */
  step: number
  files: File[]
  accountId: string | null
  batchId: string
  probe: SheetProbeDTO | null
  candidates: SheetCandidate[]
  configs: SheetConfig[]
  preview: SheetPreviewDTO | null
  result: SheetCommitResult | null
  options: CommitOptions
  loading: boolean
  error: string
  /** 上次预览的映射指纹（用于提示「映射已变化，请重新预览」）。 */
  previewFingerprint: string
  /** v1.2 R6：冻结策略（步 4 单选，默认 defer / §9.7）。 */
  freezeMode: FreezeMode
}

export const DEFAULT_OPTIONS: CommitOptions = {
  createMissingCategories: true,
  createMissingTags: true,
  skipDuplicates: true,
  skipSoftDuplicates: false,
  allowFrozen: true,
}

export function createWizardState(): WizardState {
  return {
    step: 0,
    files: [],
    accountId: null,
    batchId: '',
    probe: null,
    candidates: [],
    configs: [],
    preview: null,
    result: null,
    options: { ...DEFAULT_OPTIONS },
    loading: false,
    error: '',
    previewFingerprint: '',
    freezeMode: DEFAULT_FREEZE_MODE,
  }
}

/** 把 probe 结果展开成步 2 的候选列表：被判定为汇总表的 sheet 默认不勾选。 */
export function buildCandidates(probe: SheetProbeDTO): SheetCandidate[] {
  const out: SheetCandidate[] = []
  for (const file of probe.files) {
    for (const sheet of file.sheets) {
      out.push({
        fileId: file.file_id,
        fileName: file.file_name,
        fileNameSize: file.file_size,
        sheetIndex: sheet.sheet_index,
        sheetName: sheet.sheet_name,
        rowCount: sheet.row_count,
        colCount: sheet.col_count,
        columns: sheet.columns ?? [],
        headerRow: sheet.header_row || 1,
        headerRowCandidates: sheet.header_row_candidates?.length ? sheet.header_row_candidates : [1],
        sampleRows: sheet.sample_rows ?? [],
        dateHasTime: Boolean(sheet.date_has_time),
        suggestedSkip: Boolean(sheet.suggested_skip),
        skipReason: sheet.skip_reason ?? '',
        suggestedMapping: normalizeMapping(sheet.suggested_mapping),
        suggestedDirection: sheet.suggested_direction === 'income' ? 'income' : 'expense',
        selected: !sheet.suggested_skip,
      })
    }
  }
  return out
}

export function configKey(fileId: string, sheetIndex: number): string {
  return `${fileId}:${sheetIndex}`
}

/**
 * 同步已选表单 → 映射配置：
 * - 新增的已选表单按建议映射初始化；
 * - 已存在的配置保留用户改动（回退到步 3 不丢状态，T06 验收 ⑤）。
 */
export function syncConfigs(
  candidates: SheetCandidate[],
  existing: SheetConfig[],
  globalPrecision: TimePrecision,
): SheetConfig[] {
  const byKey = new Map(existing.map((c) => [c.key, c]))
  const out: SheetConfig[] = []
  for (const cand of candidates) {
    if (!cand.selected) continue
    const key = configKey(cand.fileId, cand.sheetIndex)
    const prev = byKey.get(key)
    if (prev) {
      // 表头行可能在步 2 被改过，这里以候选为准，其余（映射/方向/精度）保留
      prev.headerRow = cand.headerRow
      prev.headerRowCandidates = cand.headerRowCandidates
      prev.columns = cand.columns
      prev.rowCount = cand.rowCount
      out.push(prev)
      continue
    }
    const mapping = { ...cand.suggestedMapping }
    out.push({
      key,
      fileId: cand.fileId,
      fileName: cand.fileName,
      sheetIndex: cand.sheetIndex,
      sheetName: cand.sheetName,
      rowCount: cand.rowCount,
      columns: cand.columns,
      headerRow: cand.headerRow,
      headerRowCandidates: cand.headerRowCandidates,
      mapping,
      suggestedMapping: { ...cand.suggestedMapping },
      directionMode: mapping.direction ? 'column' : 'form_default',
      directionDefault: cand.suggestedDirection,
      timePrecision: pickPrecision(cand.dateHasTime, globalPrecision),
      dateHasTime: cand.dateHasTime,
    })
  }
  return out
}

/** 已选表单的分组（按文件），供步 2 / 步 3 渲染。 */
export function groupByFile(candidates: SheetCandidate[]): { fileId: string; fileName: string; sheets: SheetCandidate[] }[] {
  const map = new Map<string, { fileId: string; fileName: string; sheets: SheetCandidate[] }>()
  for (const c of candidates) {
    const hit = map.get(c.fileId)
    if (hit) hit.sheets.push(c)
    else map.set(c.fileId, { fileId: c.fileId, fileName: c.fileName, sheets: [c] })
  }
  return [...map.values()]
}

/** 映射配置的稳定指纹：用于判断预览是否过期。 */
export function fingerprint(configs: SheetConfig[]): string {
  return configs
    .map((c) =>
      [
        c.key,
        c.headerRow,
        c.directionMode,
        c.directionDefault,
        c.timePrecision,
        Object.values(c.mapping).map((v) => v ?? '').join(','),
      ].join('|'),
    )
    .join(';')
}

/** 组装 preview 请求（§2.3）。 */
export function buildPreviewReq(state: WizardState): SheetPreviewReq {
  return {
    batch_id: state.batchId,
    defaults: { account_id: state.accountId },
    sheets: state.configs.map((c) => ({
      file_id: c.fileId,
      sheet_index: c.sheetIndex,
      header_row: c.headerRow,
      skip: false,
      mapping: { ...c.mapping },
      direction_rule: {
        mode: c.directionMode,
        default: c.directionDefault,
        negative_is_expense: true,
      },
      category_split: null,
      time_precision: c.timePrecision,
    })),
  }
}

/** 组装 commit 请求（§2.4）：row_keys 为空 = 全部可解析行。 */
export function buildCommitReq(state: WizardState): SheetCommitReq {
  return {
    batch_id: state.batchId,
    sheets: state.configs.map((c) => ({ file_id: c.fileId, sheet_index: c.sheetIndex })),
    row_keys: [],
    defaults: {
      account_id: state.accountId,
      category_id: null,
      tag_ids: [],
      time_precision: state.configs[0]?.timePrecision ?? 'day',
    },
    create_missing_categories: state.options.createMissingCategories,
    create_missing_tags: state.options.createMissingTags,
    skip_duplicates: state.options.skipDuplicates,
    skip_soft_duplicates: state.options.skipSoftDuplicates,
    allow_frozen: state.options.allowFrozen,
    freeze_mode: state.freezeMode,
  }
}

/** 本次导入预计会「待确认冻结」的行数（= 预览里的 frozen_count，defer 才有意义）。 */
export function previewFrozenCount(state: WizardState): number {
  return state.preview?.summary.frozen_count ?? 0
}

/** 结果页跳转用的日期区间（取预览里的最小 / 最大日期）。 */
export function resultDateRange(preview: SheetPreviewDTO | null): { from: string; to: string } {
  if (!preview) return { from: '', to: '' }
  const starts: string[] = []
  const ends: string[] = []
  for (const s of preview.sheets) {
    if (s.date_range.length === 2) {
      starts.push(s.date_range[0])
      ends.push(s.date_range[1])
    }
  }
  if (!starts.length) return { from: '', to: '' }
  return { from: starts.sort()[0], to: ends.sort()[ends.length - 1] }
}

/** 步 4 的提交按钮文案：随计数变化（§5.1）。 */
export function commitButtonText(preview: SheetPreviewDTO | null): string {
  if (!preview) return '提交导入'
  const ok = preview.summary.parsed_ok - preview.summary.dup_count
  return `提交导入（约 ${Math.max(ok, 0)} 笔可创建）`
}
