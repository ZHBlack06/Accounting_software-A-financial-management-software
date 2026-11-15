/**
 * 开发用 mock 适配器：仅在 VITE_MOCK=true 时挂载，覆盖本批次页面用到的端点。
 * 交付构建默认关闭（VITE_MOCK 缺省为 false）。
 */
import type { AxiosAdapter, AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import dayjs from 'dayjs'
import type { AccountDTO, CategoryNode, TagDTO, MerchantDTO, TransactionDTO, MetaDTO } from './types'

const FMT = 'YYYY-MM-DD HH:mm:ss'

const accounts: AccountDTO[] = [
  mkAccount('acc_a1b2c3d4', '招商银行借记卡1', '1234', 'current', 100000),
  mkAccount('acc_b2c3d4e5', '支付宝余额宝', '', 'yuebao', 50000),
  mkAccount('acc_c3d4e5f6', '微信零钱', '', 'wallet', 0),
]

function mkAccount(id: string, name: string, tail: string, valueType: string, init: number): AccountDTO {
  return {
    id, name, alias: '', display_name: tail ? `${name}（${tail}）` : name,
    account_type: 'asset', value_type: valueType, bank_name: '', card_tail: tail,
    currency: 'CNY', initial_balance_cents: init, initial_balance_time: '2026-01-01 00:00:00',
    is_count_in_total: 1, balance_cents: init, sort_order: 10, is_archived: 0, remark: '',
    last_transaction_time: null, transaction_count: 0,
    created_at: '2026-01-01 00:00:00', updated_at: '2026-01-01 00:00:00',
  }
}

const categories: CategoryNode[] = [
  {
    id: 'cat_food0001', name: '餐饮', parent_id: null, level: 1, transaction_type: 'expense',
    color: '#F2994A', icon: 'food', sort_order: 10, is_archived: 0,
    children: [
      { id: 'cat_break001', name: '早餐', parent_id: 'cat_food0001', level: 2, transaction_type: 'expense', color: '#F2994A', icon: '', sort_order: 10, is_archived: 0 },
      { id: 'cat_lunch001', name: '午餐', parent_id: 'cat_food0001', level: 2, transaction_type: 'expense', color: '#F2994A', icon: '', sort_order: 20, is_archived: 0 },
      { id: 'cat_dinner01', name: '晚餐', parent_id: 'cat_food0001', level: 2, transaction_type: 'expense', color: '#F2994A', icon: '', sort_order: 30, is_archived: 0 },
    ],
  },
  {
    id: 'cat_trans001', name: '交通', parent_id: null, level: 1, transaction_type: 'expense',
    color: '#2D9CDB', icon: 'trans', sort_order: 20, is_archived: 0,
    children: [
      { id: 'cat_metro001', name: '地铁', parent_id: 'cat_trans001', level: 2, transaction_type: 'expense', color: '#2D9CDB', icon: '', sort_order: 10, is_archived: 0 },
      { id: 'cat_taxi0001', name: '打车', parent_id: 'cat_trans001', level: 2, transaction_type: 'expense', color: '#2D9CDB', icon: '', sort_order: 20, is_archived: 0 },
    ],
  },
  {
    id: 'cat_salary01', name: '工资', parent_id: null, level: 1, transaction_type: 'income',
    color: '#27AE60', icon: 'salary', sort_order: 10, is_archived: 0, children: [],
  },
]

const tags: TagDTO[] = [
  { id: 'tag_needed01', name: '必要', color: '#27AE60', use_count: 18, sort_order: 10, is_archived: 0 },
  { id: 'tag_big00001', name: '大额', color: '#EB5757', use_count: 5, sort_order: 20, is_archived: 0 },
]

const merchants: MerchantDTO[] = [
  { id: 'mch_mcd00001', name: '麦当劳', merchant_type: 'offline', platform_code: '', alias: '', region_id: null, use_count: 12, sort_order: 10, is_archived: 0 },
  { id: 'mch_meituan1', name: '美团', merchant_type: 'online', platform_code: 'meituan', alias: '', region_id: null, use_count: 30, sort_order: 20, is_archived: 0 },
]

function mkTxn(i: number, daysAgo: number, type: 'expense' | 'income', cents: number, frozen: boolean): TransactionDTO {
  const t = dayjs().subtract(daysAgo, 'day').hour(12).minute(30).second(0)
  const cat = type === 'expense' ? categories[0] : categories[2]
  return {
    id: `mock_txn_${String(i).padStart(4, '0')}`,
    created_seq: i,
    transaction_time: t.format(FMT),
    local_date: t.format('YYYY-MM-DD'),
    transaction_type: type,
    amount_cents: cents,
    signed_cents: type === 'expense' ? -cents : cents,
    currency: 'CNY',
    account: { id: accounts[0].id, name: accounts[0].display_name },
    category: { id: cat.id, name: cat.name },
    sub_category: type === 'expense' ? { id: 'cat_lunch001', name: '午餐' } : null,
    merchant: { id: merchants[0].id, name: merchants[0].name },
    merchant_text: '',
    region: null,
    tag_ids: [tags[0].id],
    tags: [{ id: tags[0].id, name: tags[0].name, color: tags[0].color }],
    counterparty_name: '',
    counterparty_account: '',
    remark: type === 'expense' ? '午餐' : '工资',
    is_adjustment: 0,
    adjustment_type: null,
    related_transaction_id: null,
    account_balance_after: null,
    total_balance_after: null,
    is_frozen: frozen,
    // v1.2 R6：最后 5 笔（i 40..44）模拟「导入的历史数据、待确认冻结」；已冻结的仍走 is_frozen
    freeze_pending: !frozen && i >= 40 && i < 45,
    freeze_confirmed_at: frozen ? dayjs().format(FMT) : null,
    import_batch_id: i >= 40 ? 'imp_mock0001' : '',
    is_materialized: frozen,
    is_deleted: false,
    deleted_at: null,
    source: 'manual',
    item_count: 0,
    items: [],
    created_at: t.unix(),
    updated_at: t.unix(),
  }
}

const txns: TransactionDTO[] = Array.from({ length: 60 }, (_, i) =>
  mkTxn(i + 1, i, i % 9 === 0 ? 'income' : 'expense', 500 + ((i * 137) % 200) * 100, i > 40),
)

const meta: MetaDTO = {
  version: '1.0.0', schema_version: 1, freeze_days: 30,
  freeze_boundary: dayjs().subtract(30, 'day').format(FMT),
  last_frozen_at: dayjs().subtract(30, 'day').format(FMT),
  current_time: dayjs().format(FMT), timezone: 'Asia/Shanghai',
  default_account_id: accounts[0].id, default_category_expense_id: 'cat_food0001',
  default_category_income_id: 'cat_salary01', default_ledger_id: 'ldg_daily001',
  default_txn_type: 'expense',
  last_used: { account_id: accounts[0].id, category_id: 'cat_food0001', merchant_id: null },
  high_amount_threshold_cents: 50000,
  ui_prefs: { theme: 'light', trend_chart_type: 'line', pie_chart_type: 'doughnut', time_preset: 'last_30d' },
  needs_rematerialize: false, ocr_available: false,
  quick_templates: [],
  time_precision: 'second',
}

function json<T>(config: InternalAxiosRequestConfig, data: T, status = 200): AxiosResponse<T> {
  return { data, status, statusText: 'OK', headers: {}, config }
}

function route(config: InternalAxiosRequestConfig): AxiosResponse {
  const url = (config.url ?? '').replace(/^\/api/, '')
  const method = (config.method ?? 'get').toLowerCase()

  if (url === '/auth/login') return json(config, { ok: true, version: '1.0.0' })
  if (url === '/auth/check') return json(config, { ok: true, expires_in: null })
  if (url === '/meta') return json(config, meta)
  if (url === '/settings' && method === 'get') {
    return json(config, { ...meta, theme: meta.ui_prefs.theme, quick_templates: meta.quick_templates ?? [] })
  }
  if (url === '/settings' && method === 'put') return json(config, JSON.parse(String(config.data ?? '{}')))
  if (url === '/health') return json(config, { status: 'ok', version: '1.0.0', db: 'ok', ocr: 'unavailable', uptime_s: 1, now: dayjs().format(FMT) })
  if (url === '/accounts') return json(config, { items: accounts, total_balance_cents: 150000 })
  if (url === '/categories') return json(config, { items: categories, total: categories.length, page: 1, page_size: 500 })
  if (url === '/tags') return json(config, { items: tags, total: tags.length, page: 1, page_size: 500 })
  if (url === '/merchants') return json(config, { items: merchants, total: merchants.length, page: 1, page_size: 20 })
  if (url === '/ledgers') return json(config, { items: [{ id: 'ldg_daily001', name: '日常', currency: 'CNY', remark: '', is_default: 1, sort_order: 0, is_archived: 0, transaction_count: 60, rules: [], created_at: '', updated_at: '' }], total: 1, page: 1, page_size: 500 })
  if (url === '/regions/search') return json(config, { items: [{ code: '310106', name: '静安区', full_name: '上海市静安区', level: 3, pinyin: 'jingan' }], total: 1, page: 1, page_size: 20 })
  if (url === '/transactions/freeze-confirm' && method === 'post') {
    const body = JSON.parse(String(config.data ?? '{}')) as {
      transaction_ids?: string[]
      batch_id?: string
      all?: boolean
    }
    let hit = 0
    for (const t of txns) {
      const byId = body.transaction_ids?.includes(t.id)
      if (t.freeze_pending && (byId || body.all || (body.batch_id && t.import_batch_id === body.batch_id))) {
        t.freeze_pending = false
        t.is_frozen = true
        t.is_materialized = true
        t.freeze_confirmed_at = dayjs().format(FMT)
        hit += 1
      }
    }
    return json(config, {
      confirmed: hit,
      pending_remaining: txns.filter((t) => t.freeze_pending).length,
      materialized: true,
      materialize_rows: hit,
      materialize_elapsed_ms: 120,
      new_frozen_at: dayjs().subtract(30, 'day').format(FMT),
      freeze_boundary: dayjs().subtract(30, 'day').format(FMT),
    })
  }
  if (url === '/transactions' && method === 'get') {
    const page = Number(config.params?.page ?? 1)
    const size = Number(config.params?.page_size ?? 50)
    const pendingOnly = String(config.params?.freeze_pending ?? '') === 'true'
    const base = pendingOnly ? txns.filter((t) => t.freeze_pending) : txns
    const items = base.slice((page - 1) * size, page * size)
    return json(config, {
      total: base.length, page, page_size: size, items,
      summary: { income_cents: 300000, expense_cents: 830050, net_cents: -530050, adjustment_net_cents: 0, count: txns.length },
    })
  }
  if (url.startsWith('/transactions/') && method === 'get') {
    const id = url.split('/')[2]
    const txn = txns.find((t) => t.id === id) ?? txns[0]
    return json(config, { transaction: txn, linked: [], adjustments: [], screenshot: null })
  }
  if (url === '/stats/summary') {
    return json(config, {
      start_time: '', end_time: '', income_cents: 1200000, expense_cents: 830050,
      net_cents: 369950, total_balance_cents: 2135700,
      adjustment_income_cents: 9000, adjustment_expense_cents: 0, adjustment_net_cents: 9000,
      transaction_count: 132, day_count: 30, avg_expense_per_day_cents: 27668,
    })
  }
  if (url === '/stats/trend') {
    const points = Array.from({ length: 30 }, (_, i) => {
      const d = dayjs().subtract(29 - i, 'day')
      const exp = ((i * 97) % 300 + 50) * 100
      const inc = i % 9 === 0 ? 300000 : 0
      return { bucket: d.format('YYYY-MM-DD'), label: d.format('MM-DD'), income_cents: inc, expense_cents: exp, net_cents: inc - exp, adjustment_cents: 0, count: 2 }
    })
    return json(config, { granularity: config.params?.granularity ?? 'day', points })
  }
  if (url === '/stats/category-breakdown') {
    return json(config, {
      type: 'expense', level: 1, total_cents: 830050,
      items: [
        { category_id: 'cat_food0001', name: '餐饮', parent_id: null, amount_cents: 320000, percent: 38.55, transaction_count: 42, children: [] },
        { category_id: 'cat_trans001', name: '交通', parent_id: null, amount_cents: 120000, percent: 14.45, transaction_count: 30, children: [] },
      ],
    })
  }
  if (url === '/stats/account-balance-series') {
    const mk = (base: number) => Array.from({ length: 30 }, (_, i) => ({ bucket: dayjs().subtract(29 - i, 'day').format('YYYY-MM-DD'), balance_cents: base + i * 1000 }))
    return json(config, {
      granularity: 'day',
      series: accounts.map((a, i) => ({ account_id: a.id, name: a.display_name, points: mk(a.initial_balance_cents + i * 5000) })),
      total_points: mk(2000000),
    })
  }
  if (url === '/stats/category-ranking') {
    return json(config, {
      items: [
        { rank: 1, category_id: 'cat_food0001', name: '餐饮', amount_cents: 320000, percent: 38.55, transaction_count: 42 },
        { rank: 2, category_id: 'cat_trans001', name: '交通', amount_cents: 120000, percent: 14.45, transaction_count: 30 },
      ],
    })
  }
  if (url === '/stats/high-amount') {
    return json(config, { threshold_cents: 50000, auto_threshold: true, items: txns.slice(0, 5), total_cents: 320000 })
  }
  if (url === '/reports/category') {
    return json(config, {
      level: Number(config.params?.level ?? 1), total_cents: 830050, exclude_adjustments: true,
      items: [
        { category_id: 'cat_food0001', name: '餐饮', amount_cents: 320000, percent: 38.55, prev_period_cents: 300000, prev_period_pct: 6.67, prev_year_cents: 280000, prev_year_pct: 14.29, transaction_count: 42 },
        { category_id: 'cat_trans001', name: '交通', amount_cents: 120000, percent: 14.45, prev_period_cents: 100000, prev_period_pct: 20, prev_year_cents: 0, prev_year_pct: null, transaction_count: 30 },
      ],
    })
  }
  if (url === '/reports/account') {
    return json(config, {
      items: accounts.map((a) => ({
        account_id: a.id, name: a.display_name, income_cents: 400000, expense_cents: 260000,
        transfer_in_cents: 50000, transfer_out_cents: 20000, net_cents: 140000, balance_cents: a.initial_balance_cents,
      })),
      total_balance_cents: 150000,
    })
  }
  if (url === '/reports/tag') {
    return json(config, {
      items: tags.map((t) => ({ tag_id: t.id, name: t.name, color: t.color, amount_cents: 210000, percent: 25.3, transaction_count: 18 })),
      total_cents: 840000,
    })
  }
  if (url === '/reports/period-compare') {
    return json(config, {
      period: config.params?.period ?? 'month',
      current: { label: '2026-10', start_time: '', end_time: '', income_cents: 120000, expense_cents: 83000, net_cents: 37000 },
      previous: { label: '2026-09', start_time: '', end_time: '', income_cents: 100000, expense_cents: 90000, net_cents: 10000 },
      prev_year: { label: '2025-10', start_time: '', end_time: '', income_cents: 95000, expense_cents: 80000, net_cents: 15000 },
      diff: { income_cents: 20000, income_pct: 20, expense_cents: -7000, expense_pct: -7.78 },
      exclude_adjustments: true,
    })
  }
  if (url === '/backup/list') {
    return json(config, { items: [{ file: 'ledger-20261003-1200.zip', size_bytes: 2457600, created_at: '2026-10-03 12:00:00', checksum: 'sha256:ab12cd34ef56' }] })
  }
  if (url === '/backup/create' && method === 'post') {
    return json(config, { file: 'ledger-mock.zip', path: 'data/backups/ledger-mock.zip', size_bytes: 2457600, created_at: dayjs().format(FMT), checksum: 'sha256:mock' }, 201)
  }
  if (url === '/import/sheet/probe' && method === 'post') {
    return json(config, {
      batch_id: 'imp_mock0001',
      warnings: ['「统计」疑似汇总表，已建议不导入'],
      files: [{
        file_id: 'fs_mock0001', file_name: '记帐.xlsx', file_size: 271360, sha1: 'mock',
        kind: 'xlsx', encoding: '', sheet_count: 2,
        sheets: [
          {
            sheet_index: 0, sheet_name: '统计', row_count: 128, col_count: 4, header_row: 1,
            header_row_candidates: [1, 2], columns: ['2018年', '餐饮', '合计', '占比'],
            sample_rows: [['2018年10月', '餐饮', '272.16', '12.3%']],
            suggested_mapping: {}, suggested_direction: 'expense', suggested_skip: true,
            skip_reason: '疑似汇总/透视表：表头无日期列且首列含「合计」', date_has_time: false,
          },
          {
            sheet_index: 1, sheet_name: '记帐', row_count: 6747, col_count: 5, header_row: 1,
            header_row_candidates: [1], columns: ['月份', '日期', '分类', '备注', '金额'],
            sample_rows: [['2018年10月', '43383', '餐饮', '午饭', '272.16']],
            suggested_mapping: { date: '日期', amount: '金额', category: '分类', remark: '备注' },
            suggested_direction: 'expense', suggested_skip: false, skip_reason: '', date_has_time: false,
          },
        ],
      }],
    })
  }
  if (url === '/import/sheet/preview' && method === 'post') {
    const ok = 6701
    return json(config, {
      batch_id: 'imp_mock0001',
      summary: { total_rows: 6746, parsed_ok: ok, skipped: 42, error: 3, dup_count: 0, soft_dup_count: 12, frozen_count: 6300 },
      sheets: [{
        file_id: 'fs_mock0001', sheet_index: 1, sheet_name: '记帐', total_rows: 6746,
        parsed_ok: ok, skipped: 42, error: 3, date_range: ['2018-10-10', '2026-11-05'],
        direction_stats: { expense: 6661, income: 40 },
        time_distribution_sample: [{ local_date: '2018-10-10', count: 3, times: ['2018-10-10 04:00:00', '2018-10-10 12:00:00', '2018-10-10 20:00:00'] }],
        errors: [
          { row_no: 37, column: '日期', value: '', reason: '日期为空' },
          { row_no: 512, column: '金额', value: '总计', reason: '金额无法解析' },
        ],
      }],
      auto_create: {
        categories: [
          { name: '其他', parent_name: null, transaction_type: 'expense', level: 1, rows: 312, exists: false },
          { name: '其它', parent_name: null, transaction_type: 'expense', level: 1, rows: 7, exists: false },
        ],
        tags: [{ name: '报销', rows: 12, exists: true }],
      },
      rows: [], rows_truncated: true,
    })
  }
  if (url === '/import/sheet/commit' && method === 'post') {
    return json(config, {
      batch_id: 'imp_mock0001', created: 6701, skipped_dup: 0, skipped_soft_dup: 0,
      skipped_invalid: 45, frozen_created: 6300, rematerialize_triggered: true,
      created_categories: [{ id: 'cat_x9f3a1c2', name: '其他' }], created_tags: [],
      transaction_ids: [], transaction_ids_omitted: true, partial: false,
      time_distribution_shift_warning: false,
    })
  }
  if (url === '/accounts/merge' && method === 'post') {
    return json(config, {
      target_account_id: accounts[0].id, created_target: true, merged_accounts: 3,
      moved_transactions: 42, moved_transfer_legs: 2, initial_balance_cents: 150000,
      rematerialized: true, rematerialize_rows: 42,
    })
  }
  if (url === '/import/csv/preview') {
    return json(config, { error: { code: 'UNSUPPORTED_CSV', message: 'mock 模式不解析 CSV，请用真实后端验证导入', details: { detected_headers: ['交易时间', '收/支'] } } }, 422)
  }
  if (url === '/ocr/recognize') {
    return json(config, { error: { code: 'OCR_UNAVAILABLE', message: 'OCR 服务未启动，可手动录入或稍后重试', details: { endpoint: 'http://127.0.0.1:8771' } } }, 503)
  }
  return json(config, { error: { code: 'NOT_FOUND', message: `mock: 未覆盖 ${method} ${url}` } }, 404)
}

export const mockAdapter: AxiosAdapter = async (config) => {
  await new Promise((r) => setTimeout(r, 80))
  return route(config)
}
