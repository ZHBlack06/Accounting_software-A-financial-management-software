<template>
  <div class="transfer-section">
    <el-alert
      type="info"
      :closable="false"
      title="内部转账：生成转出/转入两笔记录，不计入收支统计"
      style="margin-bottom: 12px"
    />
    <el-form-item label="转出账户" required :error="sameAccountError">
      <el-select
        :model-value="fromAccountId"
        placeholder="转出账户"
        style="width: 100%"
        @update:model-value="(v: string) => emit('update:fromAccountId', v)"
      >
        <el-option
          v-for="a in accounts"
          :key="a.id"
          :value="a.id"
          :label="a.display_name"
          :disabled="a.id === toAccountId"
        />
      </el-select>
    </el-form-item>
    <el-form-item label="转入账户" required :error="sameAccountError">
      <el-select
        :model-value="toAccountId"
        placeholder="转入账户"
        style="width: 100%"
        @update:model-value="(v: string) => emit('update:toAccountId', v)"
      >
        <el-option
          v-for="a in accounts"
          :key="a.id"
          :value="a.id"
          :label="a.display_name"
          :disabled="a.id === fromAccountId"
        />
      </el-select>
    </el-form-item>
  </div>
</template>

<script setup lang="ts">
/**
 * 内部转账区块：转出/转入账户。
 * 互斥：已选为转入的账户在「转出」下拉里置灰，反之亦然；同账户仍给出 error 兜底
 * （后端也会返回 400 VALIDATION_ERROR「转出与转入账户不能相同」）。
 */
import { computed } from 'vue'
import type { AccountDTO } from '@/api/types'

const props = defineProps<{
  accounts: AccountDTO[]
  fromAccountId: string | null
  toAccountId: string | null
}>()

const emit = defineEmits<{
  'update:fromAccountId': [v: string]
  'update:toAccountId': [v: string]
}>()

const sameAccountError = computed(() =>
  props.fromAccountId && props.toAccountId && props.fromAccountId === props.toAccountId
    ? '转出与转入账户不能相同'
    : '',
)
</script>
