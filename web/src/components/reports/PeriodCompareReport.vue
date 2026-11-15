<template>
  <div>
    <div class="report-toolbar">
      <el-radio-group v-model="period" size="small">
        <el-radio-button value="month">月</el-radio-button>
        <el-radio-button value="quarter">季度</el-radio-button>
        <el-radio-button value="year">年</el-radio-button>
      </el-radio-group>
      <el-date-picker
        v-model="base"
        :type="period === 'year' ? 'year' : 'month'"
        :value-format="period === 'year' ? 'YYYY' : 'YYYY-MM'"
        size="small"
        style="width: 130px"
        :clearable="false"
      />
      <el-checkbox v-model="excludeAdj">排除调整项（默认开）</el-checkbox>
    </div>

    <ChartBlock :loading="loading" :error="error" :empty="!data" @retry="load">
      <template v-if="data">
        <el-table :data="rows" size="small" style="margin-bottom: 12px">
          <el-table-column prop="label" label="周期" min-width="110" />
          <el-table-column label="收入" width="130" align="right">
            <template #default="{ row }"><MoneyText :cents="row.income_cents" type="income" /></template>
          </el-table-column>
          <el-table-column label="支出" width="130" align="right">
            <template #default="{ row }"><MoneyText :cents="row.expense_cents" type="expense" /></template>
          </el-table-column>
          <el-table-column label="结余" width="130" align="right">
            <template #default="{ row }"><MoneyText :cents="row.net_cents" type="auto" :bold="true" /></template>
          </el-table-column>
          <el-table-column label="收入差异" width="110" align="right">
            <template #default="{ row }"><PctText v-if="row.key === 'current'" :pct="data.diff.income_pct" :cents="data.diff.income_cents" /><span v-else class="text-tertiary">—</span></template>
          </el-table-column>
          <el-table-column label="支出差异" width="110" align="right">
            <template #default="{ row }"><PctText v-if="row.key === 'current'" :pct="data.diff.expense_pct" :cents="data.diff.expense_cents" /><span v-else class="text-tertiary">—</span></template>
          </el-table-column>
        </el-table>
        <BaseChart :option="chartOption" height="280px" />
      </template>
    </ChartBlock>
  </div>
</template>

<script setup lang="ts">
/** 周期对比报表（T14 验收 5）：月/季/年 + base；本期/上期/去年同期 + 差异列 + 分组柱状图。 */
import { computed, onMounted, ref, watch } from 'vue'
import dayjs from 'dayjs'
import type { EChartsCoreOption } from 'echarts'
import { fetchPeriodCompare } from '@/api/reports'
import { chartThemeVars, centsTooltip, INCOME_COLOR, EXPENSE_COLOR, PALETTE } from '@/utils/echarts'
import { useUiStore } from '@/stores/ui'
import type { PeriodCompareDTO } from '@/api/types'
import MoneyText from '@/components/common/MoneyText.vue'
import PctText from '@/components/reports/PctText.vue'
import ChartBlock from '@/components/common/ChartBlock.vue'
import BaseChart from '@/components/common/BaseChart.vue'

const ui = useUiStore()
const period = ref<'month' | 'quarter' | 'year'>('month')
const base = ref<string>(dayjs().format('YYYY-MM'))
const excludeAdj = ref(true)
const data = ref<PeriodCompareDTO | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

const rows = computed(() => {
  if (!data.value) return []
  return [
    { ...data.value.current, key: 'current', label: `本期 ${data.value.current.label}` },
    { ...data.value.previous, key: 'previous', label: `上期 ${data.value.previous.label}` },
    { ...data.value.prev_year, key: 'prev_year', label: `去年同期 ${data.value.prev_year.label}` },
  ]
})

const chartOption = computed<EChartsCoreOption>(() => {
  void ui.isDark
  const theme = chartThemeVars()
  const d = data.value
  if (!d) return {}
  return {
    backgroundColor: 'transparent',
    grid: { left: 8, right: 8, top: 32, bottom: 8, containLabel: true },
    legend: { textStyle: { color: theme.text } },
    tooltip: {
      trigger: 'axis',
      formatter: (params: unknown) => {
        const arr = params as { seriesName: string; value: number; axisValue: string }[]
        return [arr[0]?.axisValue ?? '', ...arr.map((p) => `${p.seriesName}：${centsTooltip(Math.round(p.value * 100))}`)].join('<br/>')
      },
    },
    xAxis: {
      type: 'category',
      data: [d.current.label, d.previous.label, d.prev_year.label],
      axisLabel: { color: theme.text },
      axisLine: { lineStyle: { color: theme.split } },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: theme.text, formatter: (v: number) => v.toFixed(0) },
      splitLine: { lineStyle: { color: theme.split } },
    },
    series: [
      {
        name: '收入',
        type: 'bar',
        barGap: '10%',
        itemStyle: { color: INCOME_COLOR },
        label: { show: true, position: 'top', color: theme.text, formatter: (p: unknown) => ((p as { value: number }).value).toFixed(0) },
        data: [d.current.income_cents / 100, d.previous.income_cents / 100, d.prev_year.income_cents / 100],
      },
      {
        name: '支出',
        type: 'bar',
        itemStyle: { color: EXPENSE_COLOR },
        label: { show: true, position: 'top', color: theme.text, formatter: (p: unknown) => ((p as { value: number }).value).toFixed(0) },
        data: [d.current.expense_cents / 100, d.previous.expense_cents / 100, d.prev_year.expense_cents / 100],
      },
    ],
    color: PALETTE,
  }
})

async function load(): Promise<void> {
  if (!base.value) return
  loading.value = true
  error.value = null
  try {
    data.value = await fetchPeriodCompare({
      period: period.value,
      base: base.value,
      exclude_adjustments: excludeAdj.value,
    })
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

watch(period, () => {
  // 切换周期时规整 base 格式
  base.value = period.value === 'year' ? dayjs().format('YYYY') : dayjs().format('YYYY-MM')
})
watch([period, base, excludeAdj], load)
onMounted(load)
</script>

<style scoped lang="scss">
.report-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}
</style>
