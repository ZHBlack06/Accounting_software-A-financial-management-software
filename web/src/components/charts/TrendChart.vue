<template>
  <div class="card chart-card">
    <div class="chart-card__head">
      <span class="chart-card__title">收支趋势</span>
      <div class="chart-card__tools">
        <el-radio-group :model-value="granularity" size="small" @update:model-value="emit('update:granularity', $event as Granularity)">
          <el-radio-button value="day">日</el-radio-button>
          <el-radio-button value="week">周</el-radio-button>
          <el-radio-button value="month">月</el-radio-button>
          <el-radio-button value="year">年</el-radio-button>
        </el-radio-group>
        <el-radio-group :model-value="chartType" size="small" style="margin-left: 8px" @update:model-value="emit('update:chartType', $event as 'line' | 'bar')">
          <el-radio-button value="line">折线</el-radio-button>
          <el-radio-button value="bar">柱状</el-radio-button>
        </el-radio-group>
      </div>
    </div>
    <ChartBlock :loading="loading" :error="error" :empty="!data || data.points.length === 0" @retry="emit('retry')">
      <BaseChart :option="option" height="300px" :on-click="onPointClick" />
    </ChartBlock>
  </div>
</template>

<script setup lang="ts">
/** 收支趋势图（T13 验收 3）：折线↔柱状、粒度切换、tooltip 元显示、点击下钻某日。 */
import { computed } from 'vue'
import type { EChartsCoreOption } from 'echarts'
import type { Granularity, TrendDTO } from '@/api/types'
import { chartThemeVars, centsTooltip, INCOME_COLOR, EXPENSE_COLOR, ADJ_COLOR } from '@/utils/echarts'
import BaseChart from '@/components/common/BaseChart.vue'
import ChartBlock from '@/components/common/ChartBlock.vue'
import { useUiStore } from '@/stores/ui'
import { useMetaStore } from '@/stores/meta'
import { fmtBucketLabel } from '@/utils/datetime'

const props = defineProps<{
  data: TrendDTO | null
  loading: boolean
  error: string | null
  chartType: 'line' | 'bar'
  granularity: Granularity
}>()

const emit = defineEmits<{
  'update:chartType': [v: 'line' | 'bar']
  'update:granularity': [v: Granularity]
  drill: [bucket: string]
  retry: []
}>()

const ui = useUiStore()
const meta = useMetaStore()

const option = computed<EChartsCoreOption>(() => {
  void ui.isDark // 主题切换时重算
  void meta.precision // 精度切换时重算 tooltip 文案
  const theme = chartThemeVars()
  const points = props.data?.points ?? []
  const labels = points.map((p) => p.label || p.bucket)
  const income = points.map((p) => p.income_cents / 100)
  const expense = points.map((p) => p.expense_cents / 100)
  const adjust = points.map((p) => p.adjustment_cents / 100)
  const t = props.chartType

  const opt: EChartsCoreOption = {
    backgroundColor: 'transparent',
    grid: { left: 8, right: 8, top: 32, bottom: 8, containLabel: true },
    legend: { data: ['收入', '支出', '调整净额'], textStyle: { color: theme.text } },
    tooltip: {
      trigger: 'axis',
      formatter: (params: unknown) => {
        const arr = params as { seriesName: string; value: number; axisValue: string }[]
        const bucket = points[arr[0]?.['dataIndex' as never] ?? 0]?.bucket ?? arr[0]?.axisValue ?? ''
        const lines = arr.map(
          (p) => `${p.seriesName}：${centsTooltip(Math.round((p.value ?? 0) * 100))}`,
        )
        return [fmtBucketLabel(bucket, meta.precision), ...lines].join('<br/>')
      },
    },
    xAxis: {
      type: 'category',
      data: labels,
      axisLabel: { color: theme.text },
      axisLine: { lineStyle: { color: theme.split } },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: theme.text, formatter: (v: number) => v.toFixed(0) },
      splitLine: { lineStyle: { color: theme.split } },
    },
    series: [
      { name: '收入', type: t, data: income, itemStyle: { color: INCOME_COLOR }, smooth: false },
      { name: '支出', type: t, data: expense, itemStyle: { color: EXPENSE_COLOR }, smooth: false },
      {
        name: '调整净额',
        type: 'line',
        data: adjust,
        itemStyle: { color: ADJ_COLOR },
        lineStyle: { type: 'dashed', color: ADJ_COLOR },
        symbol: 'none',
      },
    ],
  }
  if (points.length > 60) {
    opt.dataZoom = [{ type: 'inside' }, { type: 'slider', height: 16, bottom: 0 }]
  }
  return opt
})

function onPointClick(params: unknown): void {
  const p = params as { dataIndex?: number }
  const bucket = props.data?.points?.[p.dataIndex ?? -1]?.bucket
  if (bucket) emit('drill', bucket)
}
</script>
