<template>
  <el-form ref="formRef" :model="form" label-width="88px" :rules="rules" @submit.prevent>
    <el-form-item label="类型" prop="transaction_type">
      <el-radio-group v-model="form.transaction_type" :disabled="isEdit && editingIsTransfer">
        <el-radio-button value="expense">支出</el-radio-button>
        <el-radio-button value="income">收入</el-radio-button>
        <!-- 账户不足 2 个时置灰而非隐藏（隐藏会让老用户以为功能没了） -->
        <el-radio-button value="transfer" :disabled="!canTransfer">
          内部转账
        </el-radio-button>
      </el-radio-group>
      <el-tooltip :disabled="canTransfer" :content="TRANSFER_NEED_TWO_ACCOUNTS" placement="top">
        <span v-if="!canTransfer" class="form-transfer-hint">{{ TRANSFER_NEED_TWO_ACCOUNTS }}</span>
      </el-tooltip>
    </el-form-item>

    <el-form-item label="金额" prop="amountYuan">
      <el-input
        ref="amountInputRef"
        v-model="form.amountYuan"
        placeholder="0.00"
        :disabled="isEdit && editingIsTransfer"
        style="width: 220px"
        inputmode="decimal"
        @input="form.amountYuan = sanitizeYuanInput(form.amountYuan)"
        @blur="normalizeAmount"
        @keyup.enter="submit"
      >
        <template #prefix>¥</template>
      </el-input>
      <span v-if="isEdit && editingIsTransfer" class="text-tertiary" style="margin-left: 8px">
        转账金额不可直接修改，请使用调整
      </span>
    </el-form-item>

    <template v-if="form.transaction_type === 'transfer'">
      <TransferSection
        :accounts="master.activeAccounts"
        :from-account-id="form.fromAccountId"
        :to-account-id="form.toAccountId"
        @update:from-account-id="form.fromAccountId = $event"
        @update:to-account-id="form.toAccountId = $event"
      />
    </template>

    <template v-else>
      <el-form-item label="账户" prop="account_id">
        <el-select
          v-model="form.account_id"
          placeholder="选择账户"
          style="width: 100%"
          :disabled="isEdit && editingIsTransfer"
          filterable
        >
          <el-option v-for="a in master.activeAccounts" :key="a.id" :value="a.id" :label="a.display_name" />
        </el-select>
      </el-form-item>

      <el-form-item label="分类" prop="category_id">
        <el-cascader
          v-model="categoryPath"
          :options="categoryOptions"
          :props="{ checkStrictly: true, emitPath: true }"
          placeholder="选择分类"
          style="width: 100%"
          clearable
        />
      </el-form-item>
    </template>

    <el-form-item label="时间" prop="transaction_time">
      <el-date-picker
        v-model="timeModel"
        :type="timePickerType"
        :value-format="timePickerFormat"
        :placeholder="meta.isDayPrecision ? '交易日期' : '交易时间'"
        style="width: 100%"
      />
      <span v-if="meta.isDayPrecision" class="text-tertiary form-time-hint">仅记录到日</span>
    </el-form-item>

    <template v-if="form.transaction_type !== 'transfer'">
      <el-form-item label="备注">
        <el-input v-model="form.remark" maxlength="200" placeholder="备注（可空）" />
      </el-form-item>

      <el-form-item label="商户">
        <el-select
          v-model="form.merchant_id"
          placeholder="搜索商户"
          style="width: 100%"
          filterable
          remote
          clearable
          :remote-method="searchMerchant"
          :loading="merchantLoading"
        >
          <el-option v-for="m in master.merchants" :key="m.id" :value="m.id" :label="m.name" />
        </el-select>
      </el-form-item>

      <el-form-item label="标签">
        <TagSelect v-model="form.tag_ids" />
      </el-form-item>

      <el-form-item label="地区">
        <el-select
          v-model="form.region_id"
          placeholder="搜索地区（如：静安 / ja）"
          style="width: 100%"
          filterable
          remote
          clearable
          :remote-method="searchRegion"
          :loading="regionLoading"
        >
          <el-option v-for="r in regionOptions" :key="r.code" :value="r.code" :label="r.full_name" />
        </el-select>
      </el-form-item>

      <el-form-item label="对方信息">
        <div style="display: flex; gap: 8px; width: 100%">
          <el-input v-model="form.counterparty_name" placeholder="对方名称（可空）" maxlength="50" />
          <el-input v-model="form.counterparty_account" placeholder="对方账号（可空）" maxlength="50" />
        </div>
      </el-form-item>

      <el-form-item v-if="!quickMode" label="物品明细">
        <TransactionItemEditor v-model="form.items" />
      </el-form-item>
    </template>

    <el-form-item v-else label="备注">
      <el-input v-model="form.remark" maxlength="200" placeholder="转账备注（可空）" />
    </el-form-item>
  </el-form>
</template>

<script setup lang="ts">
/**
 * 完整交易表单（T12 / PRD 栏目 5）：新增与编辑复用。
 * - 金额输入：仅数字 + 一个小数点 + 两位小数；失焦规整显示，v-model 内部为分（amountCents 计算属性）。
 * - 编辑转账行：金额/账户只读并提示。
 * - 提交：由父组件调用 expose 的 submit()；409 FROZEN 时 emit 'frozen-conflict'。
 * - v1.2 T07：`time_precision === 'day'` 时时间选择器只选到日（提交保留原有时分或补 00:00:00）。
 * - v1.2 T08：`el-form-item__content` 补 `min-width: 0`，避免物品明细表把弹窗撑开（见文末样式）。
 */
import { computed, reactive, ref, watch, nextTick } from 'vue'
import { ElMessage, type FormInstance, type FormRules, type ElInput } from 'element-plus'
import dayjs from 'dayjs'
import { sanitizeYuanInput, parseYuan } from '@/utils/money'
import { DATETIME_FMT, DATE_FMT, applyPrecision } from '@/utils/datetime'
import { useMasterStore } from '@/stores/master'
import { useMetaStore } from '@/stores/meta'
import { searchRegions } from '@/api/regions'
import { createTransaction, updateTransaction } from '@/api/transactions'
import { ApiError, errorText } from '@/api/client'
import type {
  RegionDTO,
  TransactionCreatePayload,
  TransactionCreateResult,
  TransactionDTO,
  TransactionItemDTO,
  TransactionType,
} from '@/api/types'
import TransferSection from './TransferSection.vue'
import TagSelect from '@/components/transaction/TagSelect.vue'
import TransactionItemEditor from '@/components/transaction/TransactionItemEditor.vue'

interface FormState {
  transaction_type: TransactionType
  amountYuan: string
  account_id: string | null
  category_id: string | null
  sub_category_id: string | null
  transaction_time: string
  remark: string
  merchant_id: string | null
  tag_ids: string[]
  region_id: string | null
  counterparty_name: string
  counterparty_account: string
  items: Omit<TransactionItemDTO, 'id'>[]
  fromAccountId: string | null
  toAccountId: string | null
}

const props = withDefaults(
  defineProps<{
    /** 编辑模式传入原交易；不传为新增。 */
    transaction?: TransactionDTO | null
    /** 快速记账模式：隐藏物品明细等重字段。 */
    quickMode?: boolean
    /** 新增时的默认值。 */
    defaults?: { accountId?: string | null; categoryId?: string | null; subCategoryId?: string | null; merchantId?: string | null }
  }>(),
  { transaction: null, quickMode: false, defaults: undefined },
)

const emit = defineEmits<{
  saved: [result: TransactionCreateResult | null, updatedId: string | null]
  'frozen-conflict': [err: ApiError]
  dirty: [dirty: boolean]
}>()

const master = useMasterStore()
const meta = useMetaStore()
const formRef = ref<FormInstance>()
const amountInputRef = ref<InstanceType<typeof ElInput>>()
const merchantLoading = ref(false)
const regionLoading = ref(false)
const regionOptions = ref<RegionDTO[]>([])
const submitting = ref(false)

const isEdit = computed(() => props.transaction !== null)
const editingIsTransfer = computed(() => props.transaction?.transaction_type === 'transfer')

/** 内部转账需要至少两个账户（后端同账户转账返回 400）。 */
const TRANSFER_NEED_TWO_ACCOUNTS = '内部转账需要至少两个账户，请先在设置里新增'
const canTransfer = computed(() => master.activeAccounts.length >= 2)

// 账户被合并/归档到不足 2 个时，别把用户留在不可用的转账模式里
watch(canTransfer, (ok) => {
  if (!ok && form.transaction_type === 'transfer' && !editingIsTransfer.value) {
    form.transaction_type = 'expense'
  }
})

const form = reactive<FormState>(emptyForm())

function emptyForm(): FormState {
  return {
    transaction_type: 'expense',
    amountYuan: '',
    account_id: null,
    category_id: null,
    sub_category_id: null,
    transaction_time: dayjs().format(DATETIME_FMT),
    remark: '',
    merchant_id: null,
    tag_ids: [],
    region_id: null,
    counterparty_name: '',
    counterparty_account: '',
    items: [],
    fromAccountId: null,
    toAccountId: null,
  }
}

const rules: FormRules = {
  amountYuan: [
    {
      validator: (_r, v: string, cb) => {
        const cents = parseYuan(v)
        if (cents === null || cents <= 0) cb(new Error('请输入大于 0 的金额'))
        else cb()
      },
      trigger: 'blur',
    },
  ],
  account_id: [{ required: true, message: '请选择账户', trigger: 'change' }],
}

const categoryOptions = computed(() => {
  const roots = form.transaction_type === 'income' ? master.incomeCategories : master.expenseCategories
  return roots.map((c) => ({
    value: c.id,
    label: c.name,
    children: (c.children ?? []).map((s) => ({ value: s.id, label: s.name })),
  }))
})

/** el-cascader 路径值 ↔ category_id/sub_category_id。 */
const categoryPath = computed<string[] | null>({
  get() {
    if (!form.category_id) return null
    return form.sub_category_id ? [form.category_id, form.sub_category_id] : [form.category_id]
  },
  set(v) {
    form.category_id = v?.[0] ?? null
    form.sub_category_id = v?.[1] ?? null
  },
})

/**
 * v1.2 T07：`day` 模式下时间选择器只到日。
 * 回写时**保留原有时分**（源数据无时间则补 00:00:00），避免「仅到日」把已有秒级数据抹平。
 */
const timePickerType = computed<'date' | 'datetime'>(() => (meta.isDayPrecision ? 'date' : 'datetime'))
const timePickerFormat = computed<string>(() => (meta.isDayPrecision ? DATE_FMT : DATETIME_FMT))
const timeModel = computed<string>({
  get: () => (meta.isDayPrecision ? form.transaction_time.slice(0, 10) : form.transaction_time),
  set: (v: string) => {
    if (!v) {
      form.transaction_time = ''
      return
    }
    if (meta.isDayPrecision) {
      // 只替换日期部分：保留原有时分，源数据无时间时补 00:00:00（§5.4）
      const prevTime = form.transaction_time.length >= 19 ? form.transaction_time.slice(11) : ''
      const next = prevTime ? `${v.slice(0, 10)} ${prevTime}` : v.slice(0, 10)
      form.transaction_time = applyPrecision(next, 'day')
    } else {
      form.transaction_time = v
    }
  },
})

/** 类型切换：转账清空分类/标签；收支恢复默认分类。 */watch(
  () => form.transaction_type,
  (t, prev) => {
    if (t === 'transfer') {
      form.category_id = null
      form.sub_category_id = null
      form.tag_ids = []
      if (!form.fromAccountId) form.fromAccountId = form.account_id
    } else if (prev === 'transfer') {
      form.account_id = form.fromAccountId ?? form.account_id
    } else {
      form.category_id = null
      form.sub_category_id = null
    }
  },
)

watch(
  form,
  () => {
    emit('dirty', isDirty())
  },
  { deep: true },
)

function isDirty(): boolean {
  return form.amountYuan !== '' || form.remark !== '' || form.items.length > 0
}

function normalizeAmount(): void {
  const cents = parseYuan(form.amountYuan)
  if (cents !== null && cents > 0) form.amountYuan = (cents / 100).toFixed(2)
}

/** 填充（新增默认值 / 编辑预填 / 模板填充共用入口）。 */
function fill(patch: Partial<FormState>): void {
  Object.assign(form, patch)
}

function reset(): void {
  Object.assign(form, emptyForm())
  if (props.defaults) {
    form.account_id = props.defaults.accountId ?? null
    form.category_id = props.defaults.categoryId ?? null
    form.sub_category_id = props.defaults.subCategoryId ?? null
    form.merchant_id = props.defaults.merchantId ?? null
  }
  if (props.transaction) {
    const t = props.transaction
    form.transaction_type = t.transaction_type
    form.amountYuan = (t.amount_cents / 100).toFixed(2)
    form.account_id = t.account.id
    form.category_id = t.category?.id ?? null
    form.sub_category_id = t.sub_category?.id ?? null
    form.transaction_time = t.transaction_time
    form.remark = t.remark
    form.merchant_id = t.merchant?.id ?? null
    form.tag_ids = [...t.tag_ids]
    form.region_id = t.region?.code ?? null
    form.counterparty_name = t.counterparty_name
    form.counterparty_account = t.counterparty_account
    form.items = (t.items ?? []).map((it) => ({ ...it }))
    if (t.transaction_type === 'transfer') {
      if (t.transfer_direction === 'out') {
        form.fromAccountId = t.account.id
        form.toAccountId = t.counter_account?.id ?? null
      } else {
        form.fromAccountId = t.counter_account?.id ?? null
        form.toAccountId = t.account.id
      }
    }
  }
  nextTick(() => focusAmount())
}

function focusAmount(): void {
  amountInputRef.value?.focus()
}

let searchTimer: ReturnType<typeof setTimeout> | null = null
function searchMerchant(q: string): void {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(async () => {
    merchantLoading.value = true
    try {
      await master.searchMerchantOptions(q)
    } finally {
      merchantLoading.value = false
    }
  }, 300)
}

function searchRegion(q: string): void {
  if (!q) {
    regionOptions.value = []
    return
  }
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(async () => {
    regionLoading.value = true
    try {
      const res = await searchRegions(q)
      regionOptions.value = res.items
    } finally {
      regionLoading.value = false
    }
  }, 300)
}

/** 提交：新增 POST / 编辑 PUT；FROZEN 冲突时 emit 给父级弹 AdjustDialog。 */
async function submit(): Promise<boolean> {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return false
  const cents = parseYuan(form.amountYuan)
  if (cents === null || cents <= 0) return false

  if (form.transaction_type === 'transfer') {
    if (!form.fromAccountId || !form.toAccountId) {
      ElMessage.warning('请选择转出与转入账户')
      return false
    }
    if (form.fromAccountId === form.toAccountId) {
      ElMessage.warning('转出与转入账户不能相同')
      return false
    }
  } else if (!form.account_id) {
    ElMessage.warning('请选择账户')
    return false
  }

  submitting.value = true
  try {
    if (isEdit.value && props.transaction) {
      await updateTransaction(props.transaction.id, {
        transaction_time: form.transaction_time,
        amount_cents: cents,
        account_id: form.account_id ?? undefined,
        category_id: form.category_id,
        sub_category_id: form.sub_category_id,
        merchant_id: form.merchant_id,
        region_id: form.region_id,
        tag_ids: form.tag_ids,
        remark: form.remark,
        counterparty_name: form.counterparty_name,
        counterparty_account: form.counterparty_account,
        items: form.items,
      })
      emit('saved', null, props.transaction.id)
      return true
    }
    const payload: TransactionCreatePayload = {
      transaction_time: form.transaction_time,
      transaction_type: form.transaction_type,
      amount_cents: cents,
      account_id: form.transaction_type === 'transfer' ? (form.fromAccountId as string) : (form.account_id as string),
      remark: form.remark,
      items: form.transaction_type === 'transfer' ? [] : form.items,
      transfer:
        form.transaction_type === 'transfer'
          ? { from_account_id: form.fromAccountId as string, to_account_id: form.toAccountId as string }
          : null,
    }
    if (form.transaction_type !== 'transfer') {
      payload.category_id = form.category_id
      payload.sub_category_id = form.sub_category_id
      payload.merchant_id = form.merchant_id
      payload.region_id = form.region_id
      payload.tag_ids = form.tag_ids
      payload.counterparty_name = form.counterparty_name
      payload.counterparty_account = form.counterparty_account
    }
    payload.client_request_id = crypto.randomUUID()
    const result = await createTransaction(payload)
    emit('saved', result, null)
    return true
  } catch (e) {
    if (e instanceof ApiError && e.code === 'FROZEN') {
      emit('frozen-conflict', e)
      return false
    }
    ElMessage.error(errorText(e))
    return false
  } finally {
    submitting.value = false
  }
}

reset()

defineExpose({ submit, reset, fill, focusAmount, form, submitting })
</script>

<style scoped lang="scss">
/**
 * v1.2 T08（R4）：`el-form-item__content` 是 flex 子项，默认 `min-width: auto`，
 * 子元素再宽也不会收缩 → 物品明细表会把整个弹窗顶开。这里限定在本表单作用域内补 `min-width: 0`，
 * 让 `.item-editor` 的 `overflow-x: auto` 真正生效（不污染全局）。
 */
:deep(.el-form-item) {
  min-width: 0;
}

:deep(.el-form-item__content) {
  min-width: 0;
  flex-wrap: wrap;
}

.form-time-hint {
  margin-left: 8px;
  line-height: 32px;
}

.form-transfer-hint {
  margin-left: 8px;
  font-size: 12px;
  color: var(--text-3);
  line-height: 32px;
  cursor: help;
}
</style>
