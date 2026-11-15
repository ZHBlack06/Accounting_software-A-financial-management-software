<template>
  <div>
    <div class="report-toolbar">
      <el-radio-group v-model="level" size="small">
        <el-radio-button :value="1">一级分类</el-radio-button>
        <el-radio-button :value="2">二级分类</el-radio-button>
      </el-radio-group>
      <el-radio-group v-model="txnType" size="small">
        <el-radio-button value="expense">支出</el-radio-button>
        <el-radio-button value="income">收入</el-radio-button>
      </el-radio-group>
      <el-checkbox v-model="excludeAdj">排除调整项</el-checkbox>
    </div>
    <ChartBlock :loading="loading" :error="error" :empty="!data || data.items.length === 0" @retry="load">
      <el-table :data="data?.items ?? []" size="small" @row-click="drill">
        <el-table-column label="分类" min-width="140">
          <template #default="{ row }">
            <span :style="{ color: master.categoryColor(row.category_id) ?? undefined }">●</span>
            {{ row.name }}
          </template>
        </el-table-column>
        <el-table-column label="金额" width="130" align="right">
          <template #default="{ row }"><MoneyText :cents="row.amount_cents" :bold="true" /></template>
        </el-table-column>
        <el-table-column label="占比" width="90" align="right">
          <template #default="{ row }">{{ row.percent.toFixed(2) }}%</template>
        </el-table-column>
        <el-table-column label="环比" width="110" align="right">
          <template #default="{ row }"><PctText :pct="row.prev_period_pct" :cents="row.prev_period_cents" /></template>
        </el-table-column>
        <el-table-column label="同比" width="110" align="right">
          <template #default="{ row }"><PctText :pct="row.prev_year_pct" :cents="row.prev_year_cents" /></template>
        </el-table-column>
        <el-table-column label="笔数" width="80" align="right" prop="transaction_count" />
      </el-table>
      <div class="report-total">合计 <MoneyText :cents="data?.total_cents ?? 0" :bold="true" /></div>
    </ChartBlock>
  </div>
</template>

<script setup lang="ts">
/** 分类汇总报表（T14 验收 2）：一级/二级、占比、环比/同比（升绿降红）、排除调整项、点击钻取明细。 */
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { fetchCategoryReport } from '@/api/reports'
import { useUiStore } from '@/stores/ui'
import { useMasterStore } from '@/stores/master'
import type { CategoryReportDTO, CategoryReportItem } from '@/api/types'
import MoneyText from '@/components/common/MoneyText.vue'
import ChartBlock from '@/components/common/ChartBlock.vue'
import PctText from '@/components/reports/PctText.vue'

const ui = useUiStore()
const master = useMasterStore()
const router = useRouter()

const level = ref<1 | 2>(1)
const txnType = ref<'expense' | 'income'>('expense')
const excludeAdj = ref(true)
const data = ref<CategoryReportDTO | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

async function load(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    data.value = await fetchCategoryReport({
      ...ui.rangeQuery,
      level: level.value,
      type: txnType.value,
      compare: 'prev_period',
      exclude_adjustments: excludeAdj.value,
    })
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

/** 点击行钻取到 /transactions 带 query。 */
function drill(row: CategoryReportItem): void {
  void router.push({
    path: '/transactions',
    query: {
      category_id: row.category_id,
      start_date: ui.range.start.slice(0, 10),
      end_date: ui.range.end.slice(0, 10),
    },
  })
}

watch([level, txnType, excludeAdj], load)
watch(() => [ui.timePreset, ui.customRange], load, { deep: true })

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

.report-total {
  margin-top: 10px;
  padding-top: 8px;
  border-top: 1px dashed var(--border-1);
  font-size: 13px;
  color: var(--text-2);
  text-align: right;
}
</style>
