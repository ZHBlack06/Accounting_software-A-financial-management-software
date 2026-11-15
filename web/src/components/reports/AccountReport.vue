<template>
  <ChartBlock :loading="loading" :error="error" :empty="!data || data.items.length === 0" @retry="load">
    <el-table :data="data?.items ?? []" size="small">
      <el-table-column prop="name" label="账户" min-width="160" />
      <el-table-column label="收入" width="120" align="right">
        <template #default="{ row }"><MoneyText :cents="row.income_cents" type="income" /></template>
      </el-table-column>
      <el-table-column label="支出" width="120" align="right">
        <template #default="{ row }"><MoneyText :cents="row.expense_cents" type="expense" /></template>
      </el-table-column>
      <el-table-column label="转入" width="110" align="right">
        <template #default="{ row }"><MoneyText :cents="row.transfer_in_cents" type="transfer" /></template>
      </el-table-column>
      <el-table-column label="转出" width="110" align="right">
        <template #default="{ row }"><MoneyText :cents="row.transfer_out_cents" type="transfer" /></template>
      </el-table-column>
      <el-table-column label="净额" width="120" align="right">
        <template #default="{ row }"><MoneyText :cents="row.net_cents" type="auto" /></template>
      </el-table-column>
      <el-table-column label="当前余额" width="130" align="right">
        <template #default="{ row }"><MoneyText :cents="row.balance_cents" :bold="true" /></template>
      </el-table-column>
    </el-table>
    <div class="report-total">
      总余额 <MoneyText :cents="data?.total_balance_cents ?? 0" :bold="true" />
      <span class="text-tertiary" style="margin-left: 12px">
        与账户总余额{{ consistent ? '一致' : '不一致（请检查数据）' }}
      </span>
    </div>
  </ChartBlock>
</template>

<script setup lang="ts">
/** 账户汇总报表（T14 验收 3）：底行合计与 meta/accounts 总余额一致校验。 */
import { computed, onMounted, ref, watch } from 'vue'
import { fetchAccountReport } from '@/api/reports'
import { useUiStore } from '@/stores/ui'
import { useMasterStore } from '@/stores/master'
import type { AccountReportDTO } from '@/api/types'
import MoneyText from '@/components/common/MoneyText.vue'
import ChartBlock from '@/components/common/ChartBlock.vue'

const ui = useUiStore()
const master = useMasterStore()
const data = ref<AccountReportDTO | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

const consistent = computed(() => {
  if (!data.value || !master.loaded.accounts) return true
  return data.value.total_balance_cents === master.totalBalanceCents
})

async function load(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    data.value = await fetchAccountReport(ui.rangeQuery)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

watch(() => [ui.timePreset, ui.customRange], load, { deep: true })
onMounted(load)
</script>

<style scoped lang="scss">
.report-total {
  margin-top: 10px;
  padding-top: 8px;
  border-top: 1px dashed var(--border-1);
  font-size: 13px;
  color: var(--text-2);
  text-align: right;
}
</style>
