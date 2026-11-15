/**
 * 时间格式化（ARCHITECTURE §10.3 + ARCHITECTURE-V12 §5.4）：统一入口，组件内禁止手写 toLocaleString。
 * API 出入格式：'YYYY-MM-DD HH:mm:ss'（Asia/Shanghai 本地）。
 */
import dayjs, { type Dayjs } from 'dayjs'
import type { TimePrecision } from '@/api/types'

export const DATETIME_FMT = 'YYYY-MM-DD HH:mm:ss'
export const DATE_FMT = 'YYYY-MM-DD'

/** 全局默认精度：与后端 settings.time_precision 缺省值一致。 */
export const DEFAULT_TIME_PRECISION: TimePrecision = 'second'

export function now(): string {
  return dayjs().format(DATETIME_FMT)
}

export function fmtDateTime(v: string | number | Dayjs | Date): string {
  return dayjs(v).format(DATETIME_FMT)
}

export function fmtDate(v: string | number | Dayjs | Date): string {
  return dayjs(v).format(DATE_FMT)
}

/** 列表展示用：'MM-DD HH:mm'，跨年补年份。 */
export function fmtShort(v: string): string {
  const d = dayjs(v)
  return d.year() === dayjs().year() ? d.format('MM-DD HH:mm') : d.format('YYYY-MM-DD HH:mm')
}

/**
 * 列表 / 卡片 / 报表行内的时间展示（v1.2 §5.4）。
 * - `second`：沿用 fmtShort（'MM-DD HH:mm'，跨年补年份）。
 * - `day`：只显示 'YYYY-MM-DD'，**不出现时间分隔符、也不出现 00:00:00**。
 */
/**
 * 列表 / 卡片 / 报表行内的时间展示（v1.2 §5.4，v1.2.1 调整）：
 * - `second`：完整 `YYYY-MM-DD HH:mm:ss`（用户要求「显示到秒」即年月日时分秒）；
 * - `day`：只显示 `YYYY-MM-DD`，**不出现时间分隔符、也不出现 00:00:00**。
 */
export function fmtTxnTime(v: string, precision: TimePrecision = DEFAULT_TIME_PRECISION): string {
  if (!v) return '—'
  if (precision === 'day') return fmtDate(v)
  return fmtDateTime(v)
}

/**
 * 详情面板的完整时间展示（v1.2 §5.4）。
 * - `second`：'YYYY-MM-DD HH:mm:ss'；`day`：'YYYY-MM-DD'。
 */
export function fmtFullTxnTime(v: string, precision: TimePrecision = DEFAULT_TIME_PRECISION): string {
  if (!v) return '—'
  return precision === 'day' ? fmtDate(v) : fmtDateTime(v)
}

/**
 * 图表 tooltip 的分桶文案（统计接口的 bucket 可能是 'YYYY-MM-DD' / 'YYYY-W10' / 'YYYY-MM' / 'YYYY'）。
 * 仅当 bucket 自带时分秒时才需要按精度裁剪，纯日期桶原样返回，避免造假时间。
 */
export function fmtBucketLabel(bucket: string, precision: TimePrecision = DEFAULT_TIME_PRECISION): string {
  if (!bucket) return '—'
  const hasTime = bucket.length > 10 && bucket.includes(':')
  if (!hasTime) return bucket
  return precision === 'day' ? bucket.slice(0, 10) : bucket
}

/** 开始日期补 00:00:00；结束日期补 23:59:59（纯日期入参时）。 */
export function toStartOfDay(date: string): string {
  return date.length === 10 ? `${date} 00:00:00` : date
}

export function toEndOfDay(date: string): string {
  return date.length === 10 ? `${date} 23:59:59` : date
}

/**
 * 按精度把已有时刻规整为可提交的 'YYYY-MM-DD HH:mm:ss'（v1.2 §5.4）：
 * `day` 模式保留已有时分（无则补 00:00:00），只替换日期部分；`second` 模式原样返回。
 */
export function applyPrecision(value: string, precision: TimePrecision): string {
  if (precision !== 'day' || !value) return value
  const timePart = value.length >= 19 ? value.slice(11, 19) : '00:00:00'
  return `${value.slice(0, 10)} ${timePart}`
}
