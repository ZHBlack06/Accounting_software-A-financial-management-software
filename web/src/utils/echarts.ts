/**
 * ECharts 统一入口（ARCHITECTURE §7.5）：调色板、主题跟随工具。
 * 全量引入 echarts（`echarts.init`），BaseChart 组件负责 init/resize/dispose。
 */
import * as echarts from 'echarts'

export { echarts }

export const PALETTE = [
  '#F2994A',
  '#2D9CDB',
  '#27AE60',
  '#EB5757',
  '#9B51E0',
  '#F2C94C',
  '#56CCF2',
  '#6FCF97',
  '#BB6BD9',
  '#828282',
]

/** 调整/冲销专属色（PRD 4.8） */
export const ADJ_COLOR = '#BB6BD9'

export const INCOME_COLOR = '#27AE60'
export const EXPENSE_COLOR = '#EB5757'

/** 从 CSS 变量读取当前主题图表配色（--chart-text / --chart-split）。 */
export function chartThemeVars(): { text: string; split: string } {
  const cs = getComputedStyle(document.documentElement)
  return {
    text: cs.getPropertyValue('--chart-text').trim() || '#57606a',
    split: cs.getPropertyValue('--chart-split').trim() || '#e6eaef',
  }
}

/** 金额（分）→ tooltip 元字符串。 */
export function centsTooltip(cents: number): string {
  const yuan = Math.round(cents) / 100
  return `¥${yuan.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}
