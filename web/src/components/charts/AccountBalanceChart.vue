<template>
  <div class="card chart-card">
    <div class="chart-card__head">
      <span class="chart-card__title">账户余额变化</span>
      <el-select
        :model-value="selectedIds"
        multiple
        collapse-tags
        collapse-tags-tooltip
        size="small"
        placeholder="选择账户（默认前 3 个）"
        style="width: 240px"
        @update:model-value="(v: string[]) => emit('update:selectedIds', v)"
      >
        <el-option v-for="a in accounts" :key="a.id" :value="a.id" :label="a.display_name" />
      </el-select>
    </div>
    <ChartBlock :loading="loading" :error="error" :empty="!data" @retry="emit('retry')">
      <BaseChart :option="option" height="300px" />
    </ChartBlock>
  </div>
</template>

<script setup lang="ts">
/** 账户余额多折线（T13 验收 5）：symbol:none、connectNulls、总余额粗线 + 半透明面积。 */
import { computed } from 'vue'
import type { EChartsCoreOption } from 'echarts'
import type { AccountBalanceSeriesDTO, AccountDTO } from '@/api/types'
import { chartThemeVars, centsTooltip, PALETTE } from '@/utils/echarts'
import { useUiStore } from '@/stores/ui'
import { useMetaStore } from '@/stores/meta'
import { fmtBucketLabel } from '@/utils/datetime'
import BaseChart from '@/components/common/BaseChart.vue'
import ChartBlock from '@/components/common/ChartBlock.vue'

const props = defineProps<{
  data: AccountBalanceSeriesDTO | null
  loading: boolean
  error: string | null
  accounts: AccountDTO[]
  selectedIds: string[]
}>()

const emit = defineEmits<{ 'update:selectedIds': [v: string[]]; retry: [] }>()

const ui = useUiStore()
const meta = useMetaStore()

const option = computed<EChartsCoreOption>(() => {
  void ui.isDark
  void meta.precision // 精度切换时重算 tooltip 文案
  const theme = chartThemeVars()
  const series = props.data?.series ?? []
  const totalPoints = props.data?.total_points ?? []
  const buckets = totalPoints.length
    ? totalPoints.map((p) => p.bucket)
    : (series[0]?.points ?? []).map((p) => p.bucket)

  const lines = series.map((s, i) => ({
    name: s.name,
    type: 'line' as const,
    smooth: false,
    symbol: 'none' as const,
    connectNulls: true,
    itemStyle: { color: PALETTE[i % PALETTE.length] },
    data: buckets.map((b) => {
      const pt = s.points.find((p) => p.bucket === b)
      return pt ? pt.balance_cents / 100 : null
    }),
  }))

  if (totalPoints.length) {
    lines.push({
      name: '总余额',
      type: 'line' as const,
      smooth: false,
      symbol: 'none' as const,
      connectNulls: true,
      itemStyle: { color: '#0969da' },
      lineStyle: { width: 3 },
      areaStyle: { opacity: 0.08 },
      data: totalPoints.map((p) => p.balance_cents / 100),
    } as never)
  }

  return {
    backgroundColor: 'transparent',
    grid: { left: 8, right: 8, top: 32, bottom: 8, containLabel: true },
    legend: { type: 'scroll', textStyle: { color: theme.text } },
    tooltip: {
      trigger: 'axis',
      formatter: (params: unknown) => {
        const arr = params as { seriesName: string; value: number | null; axisValue: string }[]
        const lines = arr
          .filter((p) => p.value != null)
          .map((p) => `${p.seriesName}：${centsTooltip(Math.round((p.value as number) * 100))}`)
        return [fmtBucketLabel(arr[0]?.axisValue ?? '', meta.precision), ...lines].join('<br/>')
      },
    },
    xAxis: {
      type: 'category',
      data: buckets,
      axisLabel: { color: theme.text },
      axisLine: { lineStyle: { color: theme.split } },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: theme.text, formatter: (v: number) => v.toFixed(0) },
      splitLine: { lineStyle: { color: theme.split } },
    },
    series: lines,
  }
})
</script>
