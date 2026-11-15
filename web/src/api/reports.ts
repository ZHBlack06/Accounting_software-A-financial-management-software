import { get } from './client'
import type {
  AccountReportDTO,
  CategoryReportDTO,
  PeriodCompareDTO,
  StatsQuery,
  TagReportDTO,
  TransactionType,
} from './types'

export function fetchCategoryReport(
  q: StatsQuery & { level: 1 | 2; type: TransactionType; compare?: 'prev_period' | 'prev_year' | 'none' },
): Promise<CategoryReportDTO> {
  return get<CategoryReportDTO>('/reports/category', { ...q })
}

export function fetchAccountReport(q: StatsQuery): Promise<AccountReportDTO> {
  return get<AccountReportDTO>('/reports/account', { ...q })
}

export function fetchTagReport(
  q: StatsQuery & { tag_ids?: string[]; tag_match?: 'any' | 'all' },
): Promise<TagReportDTO> {
  return get<TagReportDTO>('/reports/tag', { ...q })
}

export function fetchPeriodCompare(q: {
  period: 'month' | 'quarter' | 'year'
  base: string
  exclude_adjustments?: boolean
}): Promise<PeriodCompareDTO> {
  return get<PeriodCompareDTO>('/reports/period-compare', { ...q })
}
