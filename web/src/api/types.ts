/**
 * 前端 DTO 类型定义（ARCHITECTURE §4.3 + §5，与后端契约严格一致）。
 */

export type TransactionType = 'expense' | 'income' | 'transfer'
export type AdjustmentType = 'adjust' | 'reverse'
export type Granularity = 'day' | 'week' | 'month' | 'year'
export type SortKey = 'time_desc' | 'time_asc' | 'amount_desc' | 'amount_asc' | 'created_desc'
export type TxnSource = 'manual' | 'csv' | 'ocr' | 'adjustment' | 'import'
/** 记录时间精度（v1.2 R2 / ARCHITECTURE-V12 §2.6）：精确到秒 或 仅到日。 */
export type TimePrecision = 'second' | 'day'
/** 导入时的冻结策略（v1.2 R6 / §9.7）：defer=先核对再冻结（默认），immediate=立即冻结。 */
export type FreezeMode = 'defer' | 'immediate'

export interface Ref {
  id: string
  name: string
}

export interface ApiErrorBody {
  error: {
    code: string
    message: string
    details?: Record<string, unknown>
  }
}

export interface Paged<T> {
  total: number
  page: number
  page_size: number
  items: T[]
}

// ---------- meta / settings ----------

export interface QuickTemplate {
  id: string
  name: string
  amount_cents: number | null
  account_id: string | null
  category_id: string | null
  merchant_id: string | null
  sort_order: number
}

export interface UiPrefs {
  theme: 'light' | 'dark'
  trend_chart_type: 'line' | 'bar'
  pie_chart_type: 'doughnut' | 'pie'
  time_preset: string
}

export interface LastUsed {
  account_id: string | null
  category_id: string | null
  merchant_id: string | null
}

export interface MetaDTO {
  version: string
  /** 后端发布版本（新字段名；缺失时回退到 version，前端绝不硬编码版本号）。 */
  app_version?: string
  schema_version: number
  freeze_days: number
  freeze_boundary: string
  last_frozen_at: string
  current_time: string
  timezone: string
  default_account_id: string | null
  default_category_expense_id: string | null
  default_category_income_id: string | null
  default_ledger_id: string | null
  default_txn_type: TransactionType
  last_used: LastUsed
  high_amount_threshold_cents: number
  ui_prefs: UiPrefs
  needs_rematerialize: boolean
  ocr_available: boolean
  quick_templates?: QuickTemplate[]
  /** v1.2 新增：记录时间精度（缺省视为 'second'）。 */
  time_precision?: TimePrecision
}

export interface SettingsDTO {
  freeze_days: number
  default_account_id: string | null
  default_category_expense_id: string | null
  default_category_income_id: string | null
  default_txn_type: TransactionType
  high_amount_threshold_cents: number
  theme: 'light' | 'dark'
  ui_prefs: Partial<UiPrefs>
  quick_templates: QuickTemplate[]
  /** v1.2 新增：记录时间精度（可 PUT）。 */
  time_precision?: TimePrecision
}

// ---------- 主数据 ----------

export interface AccountDTO {
  id: string
  name: string
  alias: string
  display_name: string
  account_type: string
  value_type: string
  bank_name: string
  card_tail: string
  currency: string
  initial_balance_cents: number
  initial_balance_time: string
  is_count_in_total: 0 | 1
  balance_cents: number
  sort_order: number
  is_archived: 0 | 1
  remark: string
  last_transaction_time?: string | null
  transaction_count: number
  created_at: string
  updated_at: string
}

export interface AccountListDTO {
  items: AccountDTO[]
  total_balance_cents: number
}

export interface CategoryDTO {
  id: string
  name: string
  parent_id: string | null
  level: 1 | 2
  transaction_type: TransactionType
  color: string
  icon: string
  sort_order: number
  is_system?: 0 | 1
  is_archived: 0 | 1
}

export interface CategoryNode extends CategoryDTO {
  children: CategoryDTO[]
}

export interface TagDTO {
  id: string
  name: string
  color: string
  use_count: number
  sort_order: number
  is_archived: 0 | 1
}

export interface MerchantDTO {
  id: string
  name: string
  merchant_type: 'offline' | 'online'
  platform_code: string
  alias: string
  region_id: string | null
  use_count: number
  sort_order: number
  is_archived: 0 | 1
}

export interface RegionDTO {
  code: string
  name: string
  full_name: string
  level: number
  pinyin: string
}

export interface LedgerRuleTarget {
  target_type: 'category_l1' | 'category_l2' | 'tag' | 'account' | 'merchant'
  target_id: string
}

export interface LedgerRule {
  id: string
  match_mode: 'or' | 'all'
  targets: LedgerRuleTarget[]
}

export interface LedgerDTO {
  id: string
  name: string
  currency: string
  remark: string
  is_default: 0 | 1
  sort_order: number
  is_archived: 0 | 1
  transaction_count: number
  rules: LedgerRule[]
  created_at: string
  updated_at: string
}

// ---------- transactions ----------

export interface TransactionItemDTO {
  id?: string
  name: string
  sub_category_id?: string | null
  original_amount_cents: number
  paid_amount_cents: number
  discount_reason: string
  sort_order: number
}

export interface TransactionDTO {
  id: string
  created_seq: number
  transaction_time: string
  local_date: string
  transaction_type: TransactionType
  amount_cents: number
  signed_cents: number
  currency: string
  account: Ref
  transfer_direction?: 'out' | 'in' | null
  transfer_group_id?: string | null
  counter_account?: Ref | null
  category?: Ref | null
  sub_category?: Ref | null
  merchant?: Ref | null
  merchant_text: string
  region?: { code: string; name: string } | null
  tag_ids: string[]
  tags: { id: string; name: string; color: string }[]
  counterparty_name: string
  counterparty_account: string
  remark: string
  is_adjustment: 0 | 1
  adjustment_type?: AdjustmentType | null
  related_transaction_id?: string | null
  account_balance_after?: number | null
  total_balance_after?: number | null
  is_frozen: boolean
  /** v1.2 R6：导入的、早于冻结边界、用户尚未确认冻结的行（可写、不物化）。 */
  freeze_pending?: boolean
  /** 行级确认时刻（ISO 串），NULL = 从未经历确认动作。 */
  freeze_confirmed_at?: string | null
  /** 产生该行的导入批次 id（手工录入为空串）。 */
  import_batch_id?: string
  is_materialized: boolean
  is_deleted: boolean
  deleted_at?: number | null
  source: TxnSource
  item_count: number
  items?: TransactionItemDTO[]
  /** v1.2 新增：行级时间精度（导入的日期型记录为 'day'）。 */
  time_precision?: TimePrecision
  external_id?: string
  created_at: number
  updated_at: number
}

export interface TransactionSummary {
  income_cents: number
  expense_cents: number
  net_cents: number
  adjustment_net_cents: number
  count: number
}

export interface TransactionListDTO extends Paged<TransactionDTO> {
  summary: TransactionSummary
}

export interface TransactionDetailDTO {
  transaction: TransactionDTO
  linked: TransactionDTO[]
  adjustments: TransactionDTO[]
  screenshot: Record<string, unknown> | null
}

export interface TransactionQuery {
  page?: number
  page_size?: number
  start_time?: string
  end_time?: string
  account_id?: string[]
  category_id?: string
  include_sub?: boolean
  sub_category_id?: string
  tag_ids?: string[]
  tag_match?: 'any' | 'all'
  merchant_id?: string
  region_id?: string
  ledger_id?: string
  type?: TransactionType[]
  amount_min_cents?: number
  amount_max_cents?: number
  include_adjustments?: boolean
  only_adjustments?: boolean
  include_deleted?: boolean
  keyword?: string
  sort?: SortKey
  /** v1.2 R6：只看「待确认冻结」的行（列表筛选入口用）。 */
  freeze_pending?: boolean
}

export interface TransactionCreatePayload {
  transaction_time?: string
  transaction_type: TransactionType
  amount_cents: number
  account_id: string
  category_id?: string | null
  sub_category_id?: string | null
  merchant_id?: string | null
  merchant_text?: string
  region_id?: string | null
  counterparty_name?: string
  counterparty_account?: string
  tag_ids?: string[]
  remark?: string
  currency?: string
  source?: TxnSource
  external_id?: string
  items?: Omit<TransactionItemDTO, 'id'>[]
  transfer?: { from_account_id: string; to_account_id: string } | null
  allow_frozen?: boolean
  allow_duplicate?: boolean
  client_request_id?: string
}

export interface TransactionUpdatePayload {
  transaction_time?: string
  amount_cents?: number
  account_id?: string
  category_id?: string | null
  sub_category_id?: string | null
  merchant_id?: string | null
  merchant_text?: string
  region_id?: string | null
  tag_ids?: string[]
  remark?: string
  counterparty_name?: string
  counterparty_account?: string
  items?: Omit<TransactionItemDTO, 'id'>[]
}

export interface BalanceInfo {
  account_balance_cents: number
  total_balance_cents: number
}

export interface TxnWarning {
  frozen: boolean
  duplicate: boolean
  message: string
}

export interface TransactionCreateResult {
  transaction: TransactionDTO
  linked: TransactionDTO[]
  balances: BalanceInfo
  warning: TxnWarning
}

export interface AdjustPayload {
  mode: AdjustmentType
  new_amount_cents: number
  new_category_id?: string | null
  new_sub_category_id?: string | null
  new_merchant_text?: string
  remark: string
}

export interface AdjustResult {
  created: TransactionDTO[]
  delta_cents: number
  balances: BalanceInfo
  note: string
}

// ---------- stats ----------

export interface StatsQuery {
  start_time?: string
  end_time?: string
  account_id?: string[]
  category_id?: string
  tag_ids?: string[]
  merchant_id?: string
  /** 单值或数组（序列化为 csv）；category-breakdown / ranking 用单值。 */
  type?: TransactionType | TransactionType[]
  ledger_id?: string
  exclude_adjustments?: boolean
}

export interface StatsSummaryDTO {
  start_time: string
  end_time: string
  income_cents: number
  expense_cents: number
  net_cents: number
  total_balance_cents: number
  adjustment_income_cents: number
  adjustment_expense_cents: number
  adjustment_net_cents: number
  transaction_count: number
  day_count: number
  avg_expense_per_day_cents: number
}

export interface TrendPoint {
  bucket: string
  label: string
  income_cents: number
  expense_cents: number
  net_cents: number
  adjustment_cents: number
  count: number
}

export interface TrendDTO {
  granularity: Granularity
  downgraded?: boolean
  points: TrendPoint[]
}

export interface BreakdownChild {
  category_id: string
  name: string
  amount_cents: number
  percent: number
}

export interface BreakdownItem {
  category_id: string
  name: string
  parent_id: string | null
  amount_cents: number
  percent: number
  transaction_count: number
  children?: BreakdownChild[]
}

export interface BreakdownDTO {
  type: TransactionType
  level: 1 | 2
  total_cents: number
  items: BreakdownItem[]
}

export interface SeriesPoint {
  bucket: string
  balance_cents: number
}

export interface AccountSeries {
  account_id: string
  name: string
  points: SeriesPoint[]
}

export interface AccountBalanceSeriesDTO {
  granularity: Granularity
  series: AccountSeries[]
  total_points: SeriesPoint[]
}

export interface RankItem {
  rank: number
  category_id: string
  name: string
  amount_cents: number
  percent: number
  transaction_count: number
}

export interface HighAmountDTO {
  threshold_cents: number
  auto_threshold: boolean
  items: TransactionDTO[]
  total_cents: number
}

// ---------- system ----------

export interface HealthDTO {
  status: string
  version: string
  db: string
  ocr: string
  uptime_s: number
  now: string
}

// ---------- reports（§5.6） ----------

export interface CategoryReportItem {
  category_id: string
  name: string
  amount_cents: number
  percent: number
  prev_period_cents: number | null
  prev_period_pct: number | null
  prev_year_cents: number | null
  prev_year_pct: number | null
  transaction_count: number
}

export interface CategoryReportDTO {
  level: 1 | 2
  total_cents: number
  exclude_adjustments: boolean
  items: CategoryReportItem[]
}

export interface AccountReportItem {
  account_id: string
  name: string
  income_cents: number
  expense_cents: number
  transfer_in_cents: number
  transfer_out_cents: number
  net_cents: number
  balance_cents: number
}

export interface AccountReportDTO {
  items: AccountReportItem[]
  total_balance_cents: number
}

export interface TagReportItem {
  tag_id: string
  name: string
  color: string
  amount_cents: number
  percent: number
  transaction_count: number
}

export interface TagReportDTO {
  items: TagReportItem[]
  total_cents?: number
}

export interface PeriodSlice {
  label: string
  start_time: string
  end_time: string
  income_cents: number
  expense_cents: number
  net_cents: number
}

export interface PeriodCompareDTO {
  period: 'month' | 'quarter' | 'year'
  current: PeriodSlice
  previous: PeriodSlice
  prev_year: PeriodSlice
  diff: {
    income_cents: number
    income_pct: number | null
    expense_cents: number
    expense_pct: number | null
  }
  exclude_adjustments: boolean
}

// ---------- import（§5.7） ----------

export interface CsvParsedRow {
  transaction_time: string
  transaction_type: TransactionType
  amount_cents: number
  merchant_text: string
  remark: string
  external_id: string
  currency: string
  account_id?: string | null
  category_id?: string | null
}

export interface CsvPreviewRow {
  row_no: number
  raw: Record<string, string>
  parsed: CsvParsedRow | null
  dup: { is_dup: boolean; reason: string; matched_transaction_id?: string | null } | null
  frozen: boolean
  warnings: string[]
  need_confirm: string[]
}

export interface CsvPreviewDTO {
  batch_id: string
  source: 'wechat' | 'alipay'
  encoding: string
  columns: string[]
  mapping: Record<string, string>
  summary: {
    total_rows: number
    parsed_ok: number
    skipped: number
    dup_count: number
    frozen_count: number
  }
  rows: CsvPreviewRow[]
  suggest_defaults: { account_id: string | null; category_id: string | null }
}

/** CSV 预览行的行级覆盖（前端编辑态，对应 commit.overrides）。 */
export interface CsvRowOverride {
  transaction_type?: TransactionType
  amount_cents?: number
  category_id?: string
  merchant_text?: string
  remark?: string
}

export interface CsvCommitPayload {
  batch_id: string
  row_nos: number[]
  defaults: {
    account_id: string
    category_id?: string | null
    sub_category_id?: string | null
    tag_ids?: string[]
    merchant_type?: string
  }
  overrides?: {
    row_no: number
    transaction_type?: TransactionType
    amount_cents?: number
    category_id?: string
    merchant_text?: string
    remark?: string
  }[]
  skip_duplicates: boolean
  allow_frozen: boolean
}

export interface CsvCommitResult {
  created: number
  skipped_dup: number
  skipped_invalid: number
  frozen_created: number
  rematerialize_triggered: boolean
  transaction_ids: string[]
  batch_id: string
}

// ---------- ocr（§5.8） ----------

export interface OcrRecognizeAsyncResult {
  task_id: string
  screenshot_id: string
  status: string
  original_path: string
  compressed_path: string
}

export interface OcrResultData {
  id: string
  source: string
  transaction_time: string
  transaction_type: TransactionType
  amount_cents: number
  account: string | null
  account_id: string | null
  category: string | null
  category_id: string | null
  sub_category: string | null
  merchant: string | null
  merchant_id: string | null
  region: string | null
  tag_ids: string[]
  remark: string
  need_review: boolean
  warnings: string[]
}

export interface OcrTaskDTO {
  task_id: string
  status: 'pending' | 'processing' | 'done' | 'failed' | 'skipped'
  elapsed_ms?: number
  model_used?: string
  screenshot_id?: string
  result?: OcrResultData | null
  ocr_result_path?: string
  error?: string
}

/** OCR 结果确认后回传给表单的预填数据（前端内部类型）。 */
export interface OcrConfirmPayload {
  transaction_type: 'expense' | 'income'
  amount_cents: number
  transaction_time: string
  account_id: string | null
  category_id: string | null
  sub_category_id: string | null
  merchant_text: string
  remark: string
  tag_ids: string[]
  screenshot_id: string
}

export interface OcrModelDTO {
  current: string
  available: string[]
  stats?: { count: number; avg_ms: number; p95_ms: number; fail_rate: number }
}

// ---------- backup（§5.9） ----------

export interface BackupItem {
  file: string
  size_bytes: number
  created_at: string
  checksum: string
}

export interface BackupCreateResult extends BackupItem {
  path: string
}

export interface BackupRestoreResult {
  restored: boolean
  from: string
  verify: { ok: boolean; checked_rows: number }
}

// ---------- 冻结确认（v1.2 R6 / §9.6，端点 75） ----------

/** 三选一：transaction_ids（逐笔）/ batch_id（整批）/ all（全部待确认）。 */
export interface FreezeConfirmReq {
  transaction_ids?: string[]
  batch_id?: string
  all?: boolean
  confirm: boolean
}

export interface FreezeConfirmResult {
  confirmed: number
  /** 传了不存在的 id / 已确认过的 id 数（幂等重复确认时为 0）。 */
  skipped_missing?: number
  pending_remaining: number
  materialized: boolean
  materialize_rows: number
  materialize_elapsed_ms: number
  new_frozen_at: string
  freeze_boundary: string
  /** 仅物化失败时出现（确认已持久，不回滚）。 */
  materialize_error?: string
}

// ---------- sheet 导入（v1.2 §2.2–§2.4） ----------

/** probe 返回的表单信息（sheet_index 是唯一稳定定位键，sheet_name 仅展示）。 */
export interface SheetMetaDTO {
  sheet_index: number
  sheet_name: string
  row_count: number
  col_count: number
  header_row: number
  header_row_candidates: number[]
  columns: string[]
  sample_rows: string[][]
  suggested_mapping: SheetMapping
  suggested_direction: 'expense' | 'income'
  suggested_skip: boolean
  skip_reason: string
  date_has_time: boolean
  /** 表级附加提示（如「行数超过上限，已截断统计」）。 */
  warning?: string
}

export interface ProbeFileDTO {
  file_id: string
  file_name: string
  file_size: number
  sha1: string
  kind: string
  encoding: string
  sheet_count: number
  sheets: SheetMetaDTO[]
}

export interface SheetProbeDTO {
  batch_id: string
  files: ProbeFileDTO[]
  warnings: string[]
  /** 请求里带了 account_id 时，后端原样回显，供前端预填（非必返）。 */
  default_account_id?: string
}

/** 映射目标字段 → 来源列名（或 null 表示不导入）。 */
export interface SheetMapping {
  date: string | null
  month: string | null
  amount: string | null
  direction: string | null
  category: string | null
  sub_category: string | null
  account: string | null
  tag: string | null
  remark: string | null
  merchant: string | null
  external_id: string | null
}

export type DirectionMode = 'column' | 'form_default' | 'by_category'

export interface DirectionRuleDTO {
  mode: DirectionMode
  default: 'expense' | 'income'
  income_values?: string[]
  expense_values?: string[]
  negative_is_expense?: boolean
  income_category_names?: string[]
}

/** preview 请求中的单张表配置。 */
export interface SheetPreviewSheetReq {
  file_id: string
  sheet_index: number
  header_row: number
  skip: boolean
  mapping: SheetMapping
  direction_rule: DirectionRuleDTO
  category_split: null
  time_precision: TimePrecision
}

export interface SheetPreviewReq {
  batch_id: string
  defaults: { account_id: string | null }
  sheets: SheetPreviewSheetReq[]
}

export interface SheetPreviewSummary {
  total_rows: number
  parsed_ok: number
  skipped: number
  error: number
  dup_count: number
  soft_dup_count: number
  frozen_count: number
}

export interface SheetPreviewRowParsed {
  transaction_time: string
  local_date: string
  transaction_type: TransactionType
  amount_cents: number
  account_id: string | null
  category_name: string | null
  category_id: string | null
  sub_category_name: string | null
  sub_category_id: string | null
  tag_names: string[]
  merchant_text: string
  remark: string
  external_id: string
  /** 该行的时间来源精度（day = 源数据无时间，已按当日顺序分布）。 */
  time_precision: TimePrecision
}

export interface SheetPreviewRow {
  row_no: number
  sheet_index: number
  /** 后端 PreviewRow.file_id（多文件时用于定位来源文件）。 */
  file_id: string
  row_key: string
  raw: Record<string, string>
  parsed: SheetPreviewRowParsed | null
  dup: { is_dup: boolean } | null
  soft_dup: { is_dup: boolean; reason?: string; matched_transaction_id?: string | null } | null
  frozen: boolean
  warnings: string[]
  need_confirm: string[]
}

export interface SheetPreviewSheetResult {
  file_id: string
  sheet_index: number
  sheet_name: string
  total_rows: number
  parsed_ok: number
  skipped: number
  error: number
  date_range: [string, string] | []
  direction_stats: Record<string, number>
  time_distribution_sample: { local_date: string; count: number; times: string[] }[]
  errors: { row_no: number; column: string; value: string; reason: string }[]
}

export interface AutoCreateCategory {
  name: string
  parent_name: string | null
  transaction_type: TransactionType
  level: 1 | 2
  rows: number
  exists: boolean
}

export interface AutoCreateTag {
  name: string
  rows: number
  exists: boolean
}

export interface SheetPreviewDTO {
  batch_id: string
  summary: SheetPreviewSummary
  sheets: SheetPreviewSheetResult[]
  auto_create: { categories: AutoCreateCategory[]; tags: AutoCreateTag[] }
  rows: SheetPreviewRow[]
  rows_truncated: boolean
  time_distribution_shift_warning?: boolean
}

export interface SheetCommitReq {
  batch_id: string
  sheets: { file_id: string; sheet_index: number }[]
  row_keys: string[]
  defaults: {
    account_id: string | null
    category_id: string | null
    tag_ids: string[]
    time_precision: TimePrecision
  }
  create_missing_categories: boolean
  create_missing_tags: boolean
  skip_duplicates: boolean
  skip_soft_duplicates: boolean
  allow_frozen: boolean
  /** v1.2 R6：冻结策略，缺省按 defer 处理（后端同默认）。 */
  freeze_mode?: FreezeMode
}

export interface SheetCommitResult {
  batch_id: string
  created: number
  skipped_dup: number
  skipped_soft_dup: number
  skipped_invalid: number
  frozen_created: number
  rematerialize_triggered: boolean
  created_categories: Ref[]
  created_tags: Ref[]
  transaction_ids: string[]
  transaction_ids_omitted: boolean
  partial: boolean
  time_distribution_shift_warning: boolean
  /** defer 模式下落到「待确认冻结」的行数（后端可选返回，缺失时前端按预览估计）。 */
  pending_frozen?: number
}

// ---------- 账户合并（v1.2 §2.5 / §5.6） ----------

export interface AccountMergeReq {
  source_ids: string[]
  /** id 为空字符串时后端按 name 查找/新建（name 为空则用「总账户」，value_type 空则 wallet）。 */
  target: { id: string; name: string; value_type: string }
  delete_sources: boolean
  confirm: boolean
}

/** 单个源账户合并前后的余额追溯（后端 account_details）。 */
export interface AccountMergeDetail {
  account_id: string
  name: string
  balance_before_cents: number
  initial_balance_cents: number
  transaction_count: number
}

export interface AccountMergeResult {
  target_account_id: string
  created_target: boolean
  merged_accounts: number
  moved_transactions: number
  moved_transfer_legs: number
  initial_balance_cents: number
  rematerialized: boolean
  rematerialize_rows: number
  warnings: string[]
  account_details?: AccountMergeDetail[]
}
