<template>
  <div>
    <div class="report-toolbar">
      <el-select v-model="tagIds" multiple collapse-tags collapse-tags-tooltip placeholder="选择标签（空 = 全部）" clearable style="width: 280px">
        <el-option v-for="t in master.tags" :key="t.id" :value="t.id" :label="t.name" />
      </el-select>
      <el-radio-group v-model="tagMatch" size="small" :disabled="tagIds.length < 2">
        <el-radio-button value="any">命中任一</el-radio-button>
        <el-radio-button value="all">同时命中</el-radio-button>
      </el-radio-group>
      <span class="text-tertiary">同一交易命中多个标签时不重复计金额</span>
    </div>
    <ChartBlock :loading="loading" :error="error" :empty="!data || data.items.length === 0" @retry="load">
      <el-table :data="data?.items ?? []" size="small">
        <el-table-column label="标签" min-width="140">
          <template #default="{ row }">
            <el-tag size="small" :color="row.color + '22'" style="border-color: transparent">
              <span :style="{ color: row.color }">{{ row.name }}</span>
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="金额" width="130" align="right">
          <template #default="{ row }"><MoneyText :cents="row.amount_cents" :bold="true" /></template>
        </el-table-column>
        <el-table-column label="占比" width="90" align="right">
          <template #default="{ row }">{{ row.percent.toFixed(2) }}%</template>
        </el-table-column>
        <el-table-column label="笔数" width="80" align="right" prop="transaction_count" />
      </el-table>
      <div v-if="data?.total_cents != null" class="report-total">
        合计（去重口径）<MoneyText :cents="data.total_cents" :bold="true" />
      </div>
    </ChartBlock>
  </div>
</template>

<script setup lang="ts">
/** 标签汇总报表（T14 验收 4）：多标签 any/all 交叉筛选；合计为后端去重口径 total_cents。 */
import { onMounted, ref, watch } from 'vue'
import { fetchTagReport } from '@/api/reports'
import { useUiStore } from '@/stores/ui'
import { useMasterStore } from '@/stores/master'
import type { TagReportDTO } from '@/api/types'
import MoneyText from '@/components/common/MoneyText.vue'
import ChartBlock from '@/components/common/ChartBlock.vue'

const ui = useUiStore()
const master = useMasterStore()
const tagIds = ref<string[]>([])
const tagMatch = ref<'any' | 'all'>('any')
const data = ref<TagReportDTO | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

async function load(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    data.value = await fetchTagReport({
      ...ui.rangeQuery,
      tag_ids: tagIds.value.length ? tagIds.value : undefined,
      tag_match: tagMatch.value,
    })
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

watch([tagIds, tagMatch], load, { deep: true })
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
