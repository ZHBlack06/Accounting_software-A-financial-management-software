<template>
  <div class="card chart-card">
    <div class="chart-card__head">
      <span class="chart-card__title">分类占比</span>
      <div class="chart-card__tools">
        <el-radio-group :model-value="txnType" size="small" @update:model-value="emit('update:txnType', $event as 'expense' | 'income')">
          <el-radio-button value="expense">支出</el-radio-button>
          <el-radio-button value="income">收入</el-radio-button>
        </el-radio-group>
        <el-radio-group :model-value="pieType" size="small" style="margin-left: 8px" @update:model-value="emit('update:pieType', $event as 'doughnut' | 'pie')">
          <el-radio-button value="doughnut">环形</el-radio-button>
          <el-radio-button value="pie">饼图</el-radio-button>
        </el-radio-group>
      </div>
    </div>
    <ChartBlock :loading="loading" :error="error" :empty="!data || data.items.length === 0" @retry="emit('retry')">
      <BaseChart :option="option" height="300px" :on-click="onSliceClick" />
    </ChartBlock>
  </div>
</template>

<script setup lang="ts">
/** 分类占比图（T13 验收 4）：环形↔饼切换、中心总额、按金额降序、分类配色一致。 */
import { computed } from 'vue'
import type { EChartsCoreOption } from 'echarts'
import type { BreakdownDTO } from '@/api/types'
import { chartThemeVars, centsTooltip, PALETTE } from '@/utils/echarts'
import { formatCents } from '@/utils/money'
import { useMasterStore } from '@/stores/master'
import { useUiStore } from '@/stores/ui'
import BaseChart from '@/components/common/BaseChart.vue'
import ChartBlock from '@/components/common/ChartBlock.vue'

const props = defineProps<{
  data: BreakdownDTO | null
  loading: boolean
  error: string | null
  pieType: 'doughnut' | 'pie'
  txnType: 'expense' | 'income'
}>()

const emit = defineEmits<{
  'update:pieType': [v: 'doughnut' | 'pie']
  'update:txnType': [v: 'expense' | 'income']
  drill: [categoryId: string]
  retry: []
}>()

const master = useMasterStore()
const ui = useUiStore()

const option = computed<EChartsCoreOption>(() => {
  void ui.isDark
  const theme = chartThemeVars()
  const items = [...(props.data?.items ?? [])].sort((a, b) => b.amount_cents - a.amount_cents)
  const isDoughnut = props.pieType === 'doughnut'
  const total = props.data?.total_cents ?? 0

  return {
    backgroundColor: 'transparent',
    color: items.map((it, i) => master.categoryColor(it.category_id) ?? PALETTE[i % PALETTE.length]),
    title: isDoughnut
      ? {
          text: formatCents(total),
          subtext: '总额',
          left: 'center',
          top: '42%',
          textStyle: { fontSize: 18, color: theme.text },
          subtextStyle: { color: theme.text, fontSize: 12 },
        }
      : undefined,
    tooltip: {
      trigger: 'item',
      formatter: (p: unknown) => {
        const item = p as { name: string; value: number; percent: number }
        return `${item.name}<br/>${centsTooltip(Math.round(item.value * 100))}（${item.percent}%）`
      },
    },
    legend: {
      type: 'scroll',
      bottom: 0,
      textStyle: { color: theme.text, fontSize: 12 },
    },
    series: [
      {
        type: 'pie',
        radius: isDoughnut ? ['52%', '72%'] : '68%',
        center: ['50%', '48%'],
        itemStyle: { borderRadius: 4 },
        label: { show: false },
        labelLine: { show: false },
        data: items.map((it) => ({ name: it.name, value: it.amount_cents / 100 })),
      },
    ],
  }
})

function onSliceClick(params: unknown): void {
  const p = params as { name?: string }
  const item = props.data?.items.find((it) => it.name === p.name)
  if (item) emit('drill', item.category_id)
}
</script>
