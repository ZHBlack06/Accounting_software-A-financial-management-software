<template>
  <el-dialog
    v-model="visible"
    title="快速记账"
    width="480px"
    :close-on-click-modal="false"
    :before-close="onBeforeClose"
    append-to-body
    @opened="onOpened"
    @keydown.ctrl.enter.prevent="submit"
  >
    <TemplateBar :templates="draft.effectiveTemplates" @apply="applyTemplate" />
    <TransactionForm
      ref="formRef"
      quick-mode
      :defaults="defaults"
      @saved="onSaved"
      @frozen-conflict="onFrozenConflict"
      @dirty="dirty = $event"
    />
    <template #footer>
      <span class="quick-entry__hint">Ctrl+Enter 保存 · Esc 关闭</span>
      <el-button @click="onBeforeClose(() => {})">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">保存</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
/**
 * 快速记账对话框（T12 / PRD 栏目 5）：
 * - Ctrl+N / Alt+N 呼出（AppLayout 注册全局快捷键），金额自动聚焦，Enter 提交，Esc 关闭。
 * - 默认值：类型固定支出；账户/分类/商户取"上次使用"（draft store）。
 * - 保存成功 ElMessage 带「撤销」；warning.frozen / warning.duplicate 提示。
 * - 关闭时有未保存改动 → 确认提示。
 */
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { softDeleteTransaction } from '@/api/transactions'
import { errorText, type ApiError } from '@/api/client'
import { useDraftStore } from '@/stores/draft'
import { useMasterStore } from '@/stores/master'
import { useTransactionStore } from '@/stores/transaction'
import type { QuickTemplate, TransactionCreateResult } from '@/api/types'
import TransactionForm from './TransactionForm.vue'
import TemplateBar from './TemplateBar.vue'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [v: boolean]
  saved: []
  'frozen-conflict': [err: ApiError]
}>()

const draft = useDraftStore()
const master = useMasterStore()
const txnStore = useTransactionStore()
const formRef = ref<InstanceType<typeof TransactionForm>>()
const dirty = ref(false)
const submitting = ref(false)

const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

/** PRD 5.3 默认值：类型固定支出，账户/分类/商户取上次使用。 */
const defaults = reactive({
  accountId: null as string | null,
  categoryId: null as string | null,
  subCategoryId: null as string | null,
  merchantId: null as string | null,
})

watch(visible, (v) => {
  if (v) {
    defaults.accountId = draft.lastAccountId
    defaults.categoryId = draft.lastCategoryId
    defaults.subCategoryId = draft.lastSubCategoryId
    defaults.merchantId = draft.lastMerchantId
    dirty.value = false
    void draft.loadTemplates()
  }
})

function onOpened(): void {
  formRef.value?.reset()
  formRef.value?.focusAmount()
}

function applyTemplate(tpl: QuickTemplate): void {
  const patch: Record<string, unknown> = {}
  if (tpl.amount_cents != null) patch.amountYuan = (tpl.amount_cents / 100).toFixed(2)
  if (tpl.account_id) patch.account_id = tpl.account_id
  if (tpl.category_id) {
    // 模板分类可能是一级或二级：命中二级时同时回填一级
    const sub = master.flatCategories.find((c) => c.id === tpl.category_id && c.level === 2)
    if (sub) {
      patch.category_id = sub.parent_id
      patch.sub_category_id = sub.id
    } else {
      patch.category_id = tpl.category_id
      patch.sub_category_id = null
    }
  }
  if (tpl.merchant_id) patch.merchant_id = tpl.merchant_id
  patch.remark = tpl.name
  formRef.value?.fill(patch)
  formRef.value?.focusAmount()
}

async function submit(): Promise<void> {
  submitting.value = true
  try {
    await formRef.value?.submit()
  } finally {
    submitting.value = false
  }
}

async function onSaved(result: TransactionCreateResult | null): Promise<void> {
  visible.value = false
  if (!result) return
  const created = result.transaction
  draft.remember(created)
  txnStore.fetchList(true)

  if (result.warning.duplicate) {
    try {
      await ElMessageBox.confirm('检测到疑似重复记录，仍要保留吗？', '重复确认', {
        confirmButtonText: '保留',
        cancelButtonText: '删除',
        type: 'warning',
      })
    } catch {
      await undoCreate(created.id, true)
      return
    }
  }
  if (result.warning.frozen) {
    ElMessage.warning('该笔已进入冻结区，不可直接修改，请使用调整分录')
  }
  showUndoMessage(created.id)
}

function showUndoMessage(id: string): void {
  // 保存反馈（PRD 5.9）：成功提示 + 可撤销（撤销 = DELETE 刚建的交易）
  ElMessageBox.confirm('已保存，是否撤销这笔记录？', '保存成功', {
    confirmButtonText: '撤销',
    cancelButtonText: '保留',
    distinguishCancelAndClose: true,
    type: 'success',
  })
    .then(() => undoCreate(id, false))
    .catch(() => undefined)
}

async function undoCreate(id: string, silent: boolean): Promise<void> {
  try {
    await softDeleteTransaction(id)
    txnStore.fetchList(true)
    if (!silent) ElMessage.info('已撤销')
  } catch (e) {
    ElMessage.error(`撤销失败：${errorText(e)}`)
  }
}

function onFrozenConflict(err: ApiError): void {
  visible.value = false
  emit('frozen-conflict', err)
}

function onBeforeClose(done: () => void): void {
  if (!dirty.value) {
    visible.value = false
    done()
    return
  }
  ElMessageBox.confirm('有未保存的改动，确定关闭吗？', '提示', {
    confirmButtonText: '关闭',
    cancelButtonText: '继续编辑',
    type: 'warning',
  })
    .then(() => {
      visible.value = false
      done()
    })
    .catch(() => undefined)
}
</script>

<style scoped lang="scss">
.quick-entry__hint {
  float: left;
  font-size: 12px;
  color: var(--text-3);
  line-height: 32px;
}
</style>
