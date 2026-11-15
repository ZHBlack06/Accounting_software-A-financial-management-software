<template>
  <el-dialog
    :model-value="visible"
    title="冻结区调整"
    width="480px"
    :close-on-click-modal="false"
    append-to-body
    @update:model-value="emit('update:visible', $event)"
    @open="onOpen"
  >
    <template v-if="transaction">
      <el-alert
        type="warning"
        :closable="false"
        title="冻结区交易不可直接修改，调整将以新分录形式生效（原记录保留）"
        style="margin-bottom: 12px"
      />
      <el-form label-width="88px" @submit.prevent>
        <el-form-item label="原交易">
          <span>{{ transaction.local_date }} {{ transaction.category?.name ?? '转账' }}</span>
          <MoneyText
            :cents="transaction.amount_cents"
            :type="transaction.transaction_type"
            :signed="transaction.transaction_type !== 'transfer'"
            :bold="true"
            style="margin-left: 8px"
          />
        </el-form-item>
        <el-form-item label="方式" required>
          <el-radio-group v-model="mode">
            <el-radio value="adjust">调整分录（记差额 1 笔）</el-radio>
            <el-radio value="reverse">反冲重录（冲销 + 重录 2 笔）</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="新金额" required :error="amountError">
          <el-input
            v-model="newAmountYuan"
            style="width: 180px"
            inputmode="decimal"
            @input="newAmountYuan = sanitizeYuanInput(newAmountYuan)"
          >
            <template #prefix>¥</template>
          </el-input>
        </el-form-item>
        <el-form-item v-if="transaction.transaction_type !== 'transfer'" label="新分类">
          <el-cascader
            v-model="categoryPath"
            :options="categoryOptions"
            :props="{ checkStrictly: true }"
            clearable
            placeholder="不修改则留空"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="预计差额">
          <MoneyText :cents="deltaCents" type="auto" :bold="true" />
          <span class="text-tertiary" style="margin-left: 8px">{{ deltaHint }}</span>
        </el-form-item>
        <el-form-item label="调整备注" required :error="remarkError">
          <el-input v-model="remark" type="textarea" :rows="2" maxlength="200" placeholder="必填，说明调整原因" />
        </el-form-item>
      </el-form>
    </template>
    <template #footer>
      <el-button @click="emit('update:visible', false)">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">提交调整</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
/**
 * 调整分录对话框（T11 验收 7）：mode 单选、原金额→新金额→预计差额、remark 必填带默认模板。
 * 提交后展示 delta_cents 效果并刷新（由父级处理刷新）。
 */
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { adjustTransaction } from '@/api/transactions'
import { errorText } from '@/api/client'
import { sanitizeYuanInput, parseYuan, formatCents } from '@/utils/money'
import { useMasterStore } from '@/stores/master'
import type { AdjustmentType, TransactionDTO } from '@/api/types'
import MoneyText from '@/components/common/MoneyText.vue'

const props = defineProps<{ visible: boolean; transaction: TransactionDTO | null }>()
const emit = defineEmits<{
  'update:visible': [v: boolean]
  adjusted: [deltaCents: number]
}>()

const master = useMasterStore()
const mode = ref<AdjustmentType>('adjust')
const newAmountYuan = ref('')
const remark = ref('')
const categoryPath = ref<string[] | null>(null)
const submitting = ref(false)
const amountError = ref('')
const remarkError = ref('')

const categoryOptions = computed(() => {
  const roots = props.transaction?.transaction_type === 'income' ? master.incomeCategories : master.expenseCategories
  return roots.map((c) => ({
    value: c.id,
    label: c.name,
    children: (c.children ?? []).map((s) => ({ value: s.id, label: s.name })),
  }))
})

const newCents = computed(() => parseYuan(newAmountYuan.value))

const deltaCents = computed(() => {
  if (props.transaction === null || newCents.value === null) return 0
  return newCents.value - props.transaction.amount_cents
})

const deltaHint = computed(() => {
  if (deltaCents.value === 0) return '金额不变（adjust 将被拒绝）'
  const dir = props.transaction?.transaction_type === 'income' ? '收入' : '支出'
  return deltaCents.value > 0 ? `${dir}增加 ${formatCents(deltaCents.value)}` : `${dir}减少 ${formatCents(-deltaCents.value)}`
})

function onOpen(): void {
  const t = props.transaction
  if (!t) return
  mode.value = 'adjust'
  newAmountYuan.value = (t.amount_cents / 100).toFixed(2)
  categoryPath.value = t.category ? (t.sub_category ? [t.category.id, t.sub_category.id] : [t.category.id]) : null
  amountError.value = ''
  remarkError.value = ''
  // 默认模板：冲减 YYYY-MM-DD {分类} 多记 N 元
  const catName = t.category?.name ?? '该笔'
  remark.value = `冲减 ${t.local_date} ${catName} 多记金额`
}

async function submit(): Promise<void> {
  const t = props.transaction
  if (!t) return
  amountError.value = ''
  remarkError.value = ''
  if (newCents.value === null || newCents.value <= 0) {
    amountError.value = '请输入大于 0 的金额'
    return
  }
  if (!remark.value.trim()) {
    remarkError.value = '调整备注必填'
    return
  }
  submitting.value = true
  try {
    const res = await adjustTransaction(t.id, {
      mode: mode.value,
      new_amount_cents: newCents.value,
      new_category_id: categoryPath.value?.[0] ?? null,
      new_sub_category_id: categoryPath.value?.[1] ?? null,
      remark: remark.value.trim(),
    })
    ElMessage.success(`调整成功，差额 ${formatCents(res.delta_cents)} 已入账。${res.note ?? ''}`)
    emit('update:visible', false)
    emit('adjusted', res.delta_cents)
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    submitting.value = false
  }
}
</script>
