/**
 * 时间范围预设（ARCHITECTURE §7.4）：13 个预设 + custom。
 * 全部基于 dayjs + isoWeek（周一为一周起点），按本地日（Asia/Shanghai）含端点。
 */
import dayjs, { type Dayjs } from 'dayjs'
import isoWeek from 'dayjs/plugin/isoWeek'

dayjs.extend(isoWeek)

export type TimePresetKey =
  | 'this_week'
  | 'last_2_weeks'
  | 'last_1_month'
  | 'last_7d'
  | 'last_15d'
  | 'last_30d'
  | 'last_60d'
  | 'last_90d'
  | 'last_150d'
  | 'this_month'
  | 'last_month'
  | 'this_quarter'
  | 'this_year'
  | 'last_12_months'
  | 'custom'

export interface TimeRange {
  start: string // 'YYYY-MM-DD HH:mm:ss'
  end: string
}

export const DEFAULT_PRESET: TimePresetKey = 'last_30d'

export const PRESET_LABELS: Record<Exclude<TimePresetKey, 'custom'>, string> = {
  this_week: '本周',
  last_2_weeks: '近两周',
  last_1_month: '近一个月',
  last_7d: '7 天',
  last_15d: '15 天',
  last_30d: '30 天',
  last_60d: '60 天',
  last_90d: '90 天',
  last_150d: '150 天',
  this_month: '本月',
  last_month: '上月',
  this_quarter: '本季度',
  this_year: '本年',
  last_12_months: '最近 12 个月',
}

export const PRESET_ORDER: TimePresetKey[] = [
  'this_week',
  'last_2_weeks',
  'last_1_month',
  'last_7d',
  'last_15d',
  'last_30d',
  'last_60d',
  'last_90d',
  'last_150d',
  'this_month',
  'last_month',
  'this_quarter',
  'this_year',
  'last_12_months',
  'custom',
]

function fmt(d: Dayjs): string {
  return d.format('YYYY-MM-DD HH:mm:ss')
}

function endOfToday(now: Dayjs): Dayjs {
  return now.endOf('day')
}

/**
 * 计算预设区间。`now` 可注入以便单测锁定边界。
 * custom 时返回 null（由调用方提供 customRange）。
 */
export function resolvePreset(key: TimePresetKey, now: Dayjs = dayjs()): TimeRange | null {
  const end = endOfToday(now)
  switch (key) {
    case 'this_week':
      return { start: fmt(now.startOf('isoWeek')), end: fmt(end) }
    case 'last_2_weeks':
      return { start: fmt(now.subtract(13, 'day').startOf('day')), end: fmt(end) }
    case 'last_1_month':
      return { start: fmt(now.subtract(29, 'day').startOf('day')), end: fmt(end) }
    case 'last_7d':
      return { start: fmt(now.subtract(6, 'day').startOf('day')), end: fmt(end) }
    case 'last_15d':
      return { start: fmt(now.subtract(14, 'day').startOf('day')), end: fmt(end) }
    case 'last_30d':
      return { start: fmt(now.subtract(29, 'day').startOf('day')), end: fmt(end) }
    case 'last_60d':
      return { start: fmt(now.subtract(59, 'day').startOf('day')), end: fmt(end) }
    case 'last_90d':
      return { start: fmt(now.subtract(89, 'day').startOf('day')), end: fmt(end) }
    case 'last_150d':
      return { start: fmt(now.subtract(149, 'day').startOf('day')), end: fmt(end) }
    case 'this_month':
      return { start: fmt(now.startOf('month')), end: fmt(end) }
    case 'last_month': {
      const prev = now.subtract(1, 'month')
      return { start: fmt(prev.startOf('month')), end: fmt(prev.endOf('month')) }
    }
    case 'this_quarter': {
      const quarterStartMonth = Math.floor(now.month() / 3) * 3
      return { start: fmt(now.month(quarterStartMonth).startOf('month')), end: fmt(end) }
    }
    case 'this_year':
      return { start: fmt(now.startOf('year')), end: fmt(end) }
    case 'last_12_months':
      return { start: fmt(now.subtract(11, 'month').startOf('month')), end: fmt(end) }
    case 'custom':
      return null
    default:
      return { start: fmt(now.subtract(29, 'day').startOf('day')), end: fmt(end) }
  }
}

/** 当前范围（含 custom 兜底）。 */
export function currentRange(
  preset: TimePresetKey,
  customRange: [string, string] | null,
  now: Dayjs = dayjs(),
): TimeRange {
  if (preset === 'custom' && customRange) {
    return {
      start: `${customRange[0]} 00:00:00`,
      end: `${customRange[1]} 23:59:59`,
    }
  }
  const r = resolvePreset(preset, now)
  if (r) return r
  // custom 但未选区间 → 退回默认预设
  return resolvePreset(DEFAULT_PRESET, now) as TimeRange
}

/** 判断预设对应的合适图表粒度（区间天数 > 92 时天粒度后端会降级，前端给出建议值）。 */
export function suggestGranularity(range: TimeRange): 'day' | 'week' | 'month' | 'year' {
  const days = dayjs(range.end).diff(dayjs(range.start), 'day') + 1
  if (days <= 92) return 'day'
  if (days <= 370) return 'week'
  if (days <= 1100) return 'month'
  return 'year'
}
