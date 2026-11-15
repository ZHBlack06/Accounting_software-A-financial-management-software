<template>
  <el-dialog
    :model-value="visible"
    title="编辑交易"
    width="min(880px, 94vw)"
    class="dlg--wide"
    :close-on-click-modal="false"
    append-to-body
    @update:model-value="emit('update:visible', $event)"
  >
    <el-alert
      v-if="transaction?.transaction_type === 'transfer'"
      type="info"
      :closable="false"
      title="转账行：金额与账户不可直接修改（如需变更请使用调整），可修改时间/备注/标签"
      style="margin-bottom: 12px"
    />
    <TransactionForm
      v-if="transaction"
      ref="formRef"
      :transaction="transaction"
      @saved="onSaved"
      @frozen-conflict="onFrozenConflict"
    />
    <template #footer>
      <el-button @click="emit('update:visible', false)">取消</el-button>
      <el-button type="primary" :loading="saving" @click="submit">保存</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
/** 动态区编辑对话框（T11 验收 8/10）：保存遇 409 FROZEN 时自动切换到调整流程。 */
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { ApiError } from '@/api/client'
import type { TransactionCreateResult, TransactionDTO } from '@/api/types'
import TransactionForm from '@/components/entry/TransactionForm.vue'

const props = defineProps<{ visible: boolean; transaction: TransactionDTO | null }>()
const emit = defineEmits<{
  'update:visible': [v: boolean]
  saved: []
  /** 409 FROZEN：父级应打开 AdjustDialog。 */
  'frozen-conflict': [txn: TransactionDTO, err: ApiError]
}>()

const formRef = ref<InstanceType<typeof TransactionForm>>()
const saving = ref(false)

async function submit(): Promise<void> {
  saving.value = true
  try {
    await formRef.value?.submit()
  } finally {
    saving.value = false
  }
}

function onSaved(_result: TransactionCreateResult | null, updatedId: string | null): void {
  if (updatedId) ElMessage.success('已保存')
  emit('update:visible', false)
  emit('saved')
}

function onFrozenConflict(err: ApiError): void {
  if (props.transaction) {
    emit('update:visible', false)
    emit('frozen-conflict', props.transaction, err)
  }
}
</script>
