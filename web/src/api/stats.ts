import { get } from './client'
import type {
  AccountBalanceSeriesDTO,
  BreakdownDTO,
  Granularity,
  HighAmountDTO,
  RankItem,
  StatsQuery,
  StatsSummaryDTO,
  TransactionType,
  TrendDTO,
} from './types'

export function fetchSummary(q: StatsQuery): Promise<StatsSummaryDTO> {
  return get<StatsSummaryDTO>('/stats/summary', { ...q })
}

export function fetchTrend(q: StatsQuery & { granularity: Granularity }): Promise<TrendDTO> {
  return get<TrendDTO>('/stats/trend', { ...q })
}

export function fetchCategoryBreakdown(q: StatsQuery & { type: TransactionType; level: 1 | 2 }): Promise<BreakdownDTO> {
  return get<BreakdownDTO>('/stats/category-breakdown', { ...q })
}

export function fetchAccountBalanceSeries(
  q: StatsQuery & { account_ids?: string[]; granularity?: Granularity },
): Promise<AccountBalanceSeriesDTO> {
  const { account_ids, ...rest } = q
  return get<AccountBalanceSeriesDTO>('/stats/account-balance-series', {
    ...rest,
    account_ids: account_ids && account_ids.length ? account_ids.join(',') : undefined,
  })
}

export function fetchCategoryRanking(q: StatsQuery & { type?: TransactionType; limit?: number }): Promise<{ items: RankItem[] }> {
  return get('/stats/category-ranking', { ...q })
}

export function fetchHighAmount(q: StatsQuery & { threshold_cents?: number; limit?: number }): Promise<HighAmountDTO> {
  return get<HighAmountDTO>('/stats/high-amount', { ...q })
}
