import { defineStore } from 'pinia'
import {
  currentRange,
  DEFAULT_PRESET,
  suggestGranularity,
  type TimePresetKey,
  type TimeRange,
} from '@/utils/presets'
import { updateSettings } from '@/api/meta'
import type { TransactionType } from '@/api/types'

export interface UiFilters {
  accountIds: string[]
  categoryId: string | null
  subCategoryId: string | null
  tagIds: string[]
  merchantId: string | null
  type: TransactionType[]
  ledgerId: string | null
  amountMin: number | null // 分
  amountMax: number | null
  includeAdjustments: boolean
  keyword: string
}

interface UiState {
  theme: 'light' | 'dark'
  timePreset: TimePresetKey
  customRange: [string, string] | null
  trendChartType: 'line' | 'bar'
  pieChartType: 'doughnut' | 'pie'
  trendGranularity: 'day' | 'week' | 'month' | 'year' | 'auto'
  filters: UiFilters
  sidebarCollapsed: boolean
}

function defaultFilters(): UiFilters {
  return {
    accountIds: [],
    categoryId: null,
    subCategoryId: null,
    tagIds: [],
    merchantId: null,
    type: [],
    ledgerId: null,
    amountMin: null,
    amountMax: null,
    includeAdjustments: true,
    keyword: '',
  }
}

function loadJSON<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key)
    return raw ? (JSON.parse(raw) as T) : fallback
  } catch {
    return fallback
  }
}

export const useUiStore = defineStore('ui', {
  state: (): UiState => {
    const theme = (localStorage.getItem('theme') as 'light' | 'dark' | null) ?? 'light'
    return {
      theme,
      timePreset: loadJSON<TimePresetKey>('ui.timePreset', DEFAULT_PRESET),
      customRange: loadJSON<[string, string] | null>('ui.customRange', null),
      trendChartType: loadJSON<'line' | 'bar'>('ui.trendChartType', 'line'),
      pieChartType: loadJSON<'doughnut' | 'pie'>('ui.pieChartType', 'doughnut'),
      trendGranularity: 'auto',
      filters: defaultFilters(),
      sidebarCollapsed: false,
    }
  },
  getters: {
    /** 当前生效的时间范围（派生）。 */
    range(state): TimeRange {
      return currentRange(state.timePreset, state.customRange)
    },
    /** 供所有 API 复用的区间查询参数。 */
    rangeQuery(): { start_time: string; end_time: string } {
      return { start_time: this.range.start, end_time: this.range.end }
    },
    /** 实际使用的趋势粒度（auto 时按区间长度建议）。 */
    effectiveGranularity(state): 'day' | 'week' | 'month' | 'year' {
      if (state.trendGranularity !== 'auto') return state.trendGranularity
      return suggestGranularity(currentRange(state.timePreset, state.customRange))
    },
    isDark(state): boolean {
      return state.theme === 'dark'
    },
  },
  actions: {
    setPreset(k: TimePresetKey): void {
      this.timePreset = k
      localStorage.setItem('ui.timePreset', JSON.stringify(k))
      this.persistPrefs({ time_preset: k })
    },
    setCustom(a: string, b: string): void {
      this.customRange = [a, b]
      localStorage.setItem('ui.customRange', JSON.stringify(this.customRange))
      this.timePreset = 'custom'
      localStorage.setItem('ui.timePreset', JSON.stringify('custom'))
    },
    toggleTheme(): void {
      this.theme = this.theme === 'dark' ? 'light' : 'dark'
      this.applyTheme()
      // 契约 §5.1：theme 为 settings 顶层字段，同时写 ui_prefs 保持兼容
      updateSettings({ theme: this.theme, ui_prefs: { theme: this.theme } }).catch(() => undefined)
    },
    /** 应用主题到 <html> 并落 localStorage（刷新保持）。 */
    applyTheme(): void {
      document.documentElement.classList.toggle('dark', this.theme === 'dark')
      localStorage.setItem('theme', this.theme)
    },
    /** 从 meta.ui_prefs 同步（localStorage 优先，其次服务端偏好）。 */
    syncFromMeta(uiPrefs?: { theme?: 'light' | 'dark'; trend_chart_type?: 'line' | 'bar'; pie_chart_type?: 'doughnut' | 'pie'; time_preset?: string }): void {
      if (!uiPrefs) return
      if (!localStorage.getItem('theme') && uiPrefs.theme) {
        this.theme = uiPrefs.theme
        this.applyTheme()
      }
      if (!localStorage.getItem('ui.trendChartType') && uiPrefs.trend_chart_type) {
        this.trendChartType = uiPrefs.trend_chart_type
      }
      if (!localStorage.getItem('ui.pieChartType') && uiPrefs.pie_chart_type) {
        this.pieChartType = uiPrefs.pie_chart_type
      }
      if (!localStorage.getItem('ui.timePreset') && uiPrefs.time_preset) {
        this.timePreset = uiPrefs.time_preset as TimePresetKey
      }
    },
    setTrendChartType(t: 'line' | 'bar'): void {
      this.trendChartType = t
      localStorage.setItem('ui.trendChartType', JSON.stringify(t))
      this.persistPrefs({ trend_chart_type: t })
    },
    setPieChartType(t: 'doughnut' | 'pie'): void {
      this.pieChartType = t
      localStorage.setItem('ui.pieChartType', JSON.stringify(t))
      this.persistPrefs({ pie_chart_type: t })
    },
    setFilters(patch: Partial<UiFilters>): void {
      this.filters = { ...this.filters, ...patch }
    },
    resetFilters(): void {
      this.filters = defaultFilters()
    },
    toggleSidebar(): void {
      this.sidebarCollapsed = !this.sidebarCollapsed
    },
    /** 偏好持久化到服务端（失败静默，不阻塞交互）。 */
    persistPrefs(prefs: Record<string, unknown>): void {
      updateSettings({ ui_prefs: prefs as never }).catch(() => undefined)
    },
  },
})
