<template>
  <el-drawer :model-value="open" title="回收站" size="560px" @update:model-value="emit('update:open', $event)" @open="load">
    <el-table v-loading="loading" :data="items" size="small" empty-text="回收站为空">
      <el-table-column label="删除时间" width="150">
        <template #default="{ row }">
          <span>{{ row.deleted_at ? fmtDateTime(row.deleted_at * 1000) : '—' }}</span>
        </template>
      </el-table-column>
      <el-table-column label="交易时间" width="180">
        <template #default="{ row }">
          <TxnTime :time="row.transaction_time" :row-precision="row.time_precision" />
        </template>
      </el-table-column>
      <el-table-column label="金额" width="110" align="right">
        <template #default="{ row }">
          <MoneyText :cents="row.amount_cents" :type="row.transaction_type" :signed="row.transaction_type !== 'transfer'" />
        </template>
      </el-table-column>
      <el-table-column label="分类/备注" min-width="120">
        <template #default="{ row }">{{ row.category?.name ?? row.remark ?? '—' }}</template>
      </el-table-column>
      <el-table-column label="操作" width="150" fixed="right">
        <template #default="{ row }">
          <el-button link size="small" type="primary" @click="restore(row)">恢复</el-button>
          <el-popconfirm title="彻底删除？需删除满 180 天" @confirm="purge(row)">
            <template #reference>
              <el-button link size="small" type="danger">彻底删除</el-button>
            </template>
          </el-popconfirm>
        </template>
      </el-table-column>
    </el-table>
  </el-drawer>
</template>

<script setup lang="ts">
/** 回收站抽屉（T11 验收 11）：include_deleted 列表，恢复 / 彻底删除（未满 180 天展示 409 文案）。 */
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { listTransactions, restoreTransaction, purgeTransaction } from '@/api/transactions'
import { errorText } from '@/api/client'
import { fmtDateTime } from '@/utils/datetime'
import type { TransactionDTO } from '@/api/types'
import MoneyText from '@/components/common/MoneyText.vue'
import TxnTime from '@/components/common/TxnTime.vue'

defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [v: boolean]; changed: [] }>()

const items = ref<TransactionDTO[]>([])
const loading = ref(false)

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await listTransactions({ include_deleted: true, page: 1, page_size: 200, sort: 'time_desc' })
    items.value = res.items.filter((t) => t.is_deleted)
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    loading.value = false
  }
}

async function restore(row: TransactionDTO): Promise<void> {
  try {
    await restoreTransaction(row.id)
    ElMessage.success('已恢复')
    await load()
    emit('changed')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}

async function purge(row: TransactionDTO): Promise<void> {
  try {
    await purgeTransaction(row.id)
    ElMessage.success('已彻底删除')
    await load()
  } catch (e) {
    // 409 RETENTION 等直接展示后端中文文案
    ElMessage.error(errorText(e))
  }
}
</script>
