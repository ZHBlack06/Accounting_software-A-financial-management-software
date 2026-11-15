import { defineStore } from 'pinia'
import {
  fetchAccountBalanceSeries,
  fetchCategoryBreakdown,
  fetchCategoryRanking,
  fetchHighAmount,
  fetchSummary,
  fetchTrend,
} from '@/api/stats'
import type {
  AccountBalanceSeriesDTO,
  BreakdownDTO,
  Granularity,
  HighAmountDTO,
  RankItem,
  StatsQuery,
  StatsSummaryDTO,
  TrendDTO,
} from '@/api/types'

interface BlockState<T> {
  data: T | null
  loading: boolean
  error: string | null
}

function block<T>(): BlockState<T> {
  return { data: null, loading: false, error: null }
}

interface StatsState {
  rangeKey: string
  query: StatsQuery
  summary: BlockState<StatsSummaryDTO>
  trend: BlockState<TrendDTO>
  breakdown: BlockState<BreakdownDTO>
  breakdownLevel: 1 | 2
  breakdownType: 'expense' | 'income'
  series: BlockState<AccountBalanceSeriesDTO>
  seriesAccountIds: string[]
  ranking: BlockState<RankItem[]>
  rankingType: 'expense' | 'income'
  highAmount: BlockState<HighAmountDTO>
  seq: number
}

export const useStatsStore = defineStore('stats', {
  state: (): StatsState => ({
    rangeKey: '',
    query: {},
    summary: block<StatsSummaryDTO>(),
    trend: block<TrendDTO>(),
    breakdown: block<BreakdownDTO>(),
    breakdownLevel: 1,
    breakdownType: 'expense',
    series: block<AccountBalanceSeriesDTO>(),
    seriesAccountIds: [],
    ranking: block<RankItem[]>(),
    rankingType: 'expense',
    highAmount: block<HighAmountDTO>(),
    seq: 0,
  }),
  actions: {
    /**
     * 时间范围切换后的统一刷新入口：所有块并发请求，
     * 通过递增序号丢弃乱序响应，保证无残留旧数据。
     */
    async fetchAll(query: StatsQuery, granularity: Granularity): Promise<void> {
      this.query = { ...query }
      this.rangeKey = JSON.stringify([query.start_time, query.end_time])
      const seq = ++this.seq
      await Promise.allSettled([
        this.fetchSummary(seq),
        this.fetchTrend(granularity, seq),
        this.fetchBreakdown(seq),
        this.fetchSeries(seq),
        this.fetchRanking(seq),
        this.fetchHighAmount(seq),
      ])
    },
    async fetchSummary(seq?: number): Promise<void> {
      const s = this.summary
      s.loading = true
      s.error = null
      try {
        const data = await fetchSummary(this.query)
        if (seq !== undefined && seq !== this.seq) return
        s.data = data
      } catch (e) {
        if (seq !== undefined && seq !== this.seq) return
        s.error = e instanceof Error ? e.message : '加载失败'
      } finally {
        if (seq === undefined || seq === this.seq) s.loading = false
      }
    },
    async fetchTrend(granularity: Granularity, seq?: number): Promise<void> {
      const s = this.trend
      s.loading = true
      s.error = null
      try {
        const data = await fetchTrend({ ...this.query, granularity })
        if (seq !== undefined && seq !== this.seq) return
        s.data = data
      } catch (e) {
        if (seq !== undefined && seq !== this.seq) return
        s.error = e instanceof Error ? e.message : '加载失败'
      } finally {
        if (seq === undefined || seq === this.seq) s.loading = false
      }
    },
    async fetchBreakdown(seq?: number): Promise<void> {
      const s = this.breakdown
      s.loading = true
      s.error = null
      try {
        const data = await fetchCategoryBreakdown({
          ...this.query,
          type: this.breakdownType,
          level: this.breakdownLevel,
        })
        if (seq !== undefined && seq !== this.seq) return
        s.data = data
      } catch (e) {
        if (seq !== undefined && seq !== this.seq) return
        s.error = e instanceof Error ? e.message : '加载失败'
      } finally {
        if (seq === undefined || seq === this.seq) s.loading = false
      }
    },
    async fetchSeries(seq?: number): Promise<void> {
      const s = this.series
      s.loading = true
      s.error = null
      try {
        const data = await fetchAccountBalanceSeries({
          ...this.query,
          account_ids: this.seriesAccountIds,
          granularity: 'day',
        })
        if (seq !== undefined && seq !== this.seq) return
        s.data = data
      } catch (e) {
        if (seq !== undefined && seq !== this.seq) return
        s.error = e instanceof Error ? e.message : '加载失败'
      } finally {
        if (seq === undefined || seq === this.seq) s.loading = false
      }
    },
    setSeriesAccounts(ids: string[]): void {
      this.seriesAccountIds = ids
      void this.fetchSeries()
    },
    async fetchRanking(seq?: number): Promise<void> {
      const s = this.ranking
      s.loading = true
      s.error = null
      try {
        const data = await fetchCategoryRanking({ ...this.query, type: this.rankingType, limit: 10 })
        if (seq !== undefined && seq !== this.seq) return
        s.data = data.items
      } catch (e) {
        if (seq !== undefined && seq !== this.seq) return
        s.error = e instanceof Error ? e.message : '加载失败'
      } finally {
        if (seq === undefined || seq === this.seq) s.loading = false
      }
    },
    async fetchHighAmount(seq?: number): Promise<void> {
      const s = this.highAmount
      s.loading = true
      s.error = null
      try {
        const data = await fetchHighAmount({ ...this.query, limit: 20 })
        if (seq !== undefined && seq !== this.seq) return
        s.data = data
      } catch (e) {
        if (seq !== undefined && seq !== this.seq) return
        s.error = e instanceof Error ? e.message : '加载失败'
      } finally {
        if (seq === undefined || seq === this.seq) s.loading = false
      }
    },
  },
})
