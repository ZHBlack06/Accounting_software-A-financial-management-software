<template>
  <div class="ocr-card card">
    <div class="ocr-card__head">
      <span class="ocr-card__title">
        识别结果
        <el-tag v-if="result.need_review" size="small" type="warning" style="margin-left: 6px">需人工复核</el-tag>
      </span>
      <el-button link size="small" @click="emit('discard')">忽略</el-button>
    </div>

    <div v-if="result.warnings.length" class="ocr-card__warn">
      <div v-for="w in result.warnings" :key="w">⚠ {{ w }}</div>
    </div>

    <el-form label-width="88px" size="small">
      <el-form-item label="类型">
        <el-radio-group v-model="form.transaction_type">
          <el-radio-button value="expense">支出</el-radio-button>
          <el-radio-button value="income">收入</el-radio-button>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="金额">
        <el-input
          v-model="form.amountYuan"
          :class="{ 'field-review': review('amount') }"
          style="width: 160px"
          @input="form.amountYuan = sanitizeYuanInput(form.amountYuan)"
        >
          <template #prefix>¥</template>
        </el-input>
      </el-form-item>
      <el-form-item label="时间">
        <el-date-picker
          v-model="form.transaction_time"
          type="datetime"
          value-format="YYYY-MM-DD HH:mm:ss"
          style="width: 100%"
          :class="{ 'field-review': review('time') }"
        />
      </el-form-item>
      <el-form-item label="账户">
        <el-select v-model="form.account_id" placeholder="请选择账户" style="width: 100%" filterable>
          <el-option v-for="a in master.activeAccounts" :key="a.id" :value="a.id" :label="a.display_name" />
        </el-select>
      </el-form-item>
      <el-form-item label="分类">
        <el-cascader
          v-model="categoryPath"
          :options="categoryOptions"
          :props="{ checkStrictly: true }"
          clearable
          placeholder="请选择分类"
          style="width: 100%"
        />
      </el-form-item>
      <el-form-item label="商户">
        <el-input v-model="form.merchant_text" maxlength="50" />
      </el-form-item>
      <el-form-item label="备注">
        <el-input v-model="form.remark" maxlength="200" />
      </el-form-item>
    </el-form>

    <div v-if="previewUrl" class="ocr-card__preview">
      <img :src="previewUrl" alt="截图预览" />
    </div>

    <div class="ocr-card__actions">
      <el-button size="small" @click="emit('discard')">丢弃</el-button>
      <el-button size="small" type="primary" @click="confirm">确认并录入</el-button>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * OCR 结果卡（T15 验收 6）：字段可编辑、need_review 黄框高亮、账户/分类用选择器，
 * 确认后回传预填数据（父级打开 TransactionForm 由用户再次保存）。
 * 图片预览走 GET /api/screenshots/:id/file（经认证拉 blob → object URL）。
 */
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { fetchScreenshotUrl } from '@/api/ocr'
import { sanitizeYuanInput, parseYuan } from '@/utils/money'
import { useMasterStore } from '@/stores/master'
import type { OcrConfirmPayload, OcrResultData } from '@/api/types'

const props = defineProps<{ result: OcrResultData; screenshotId: string | null }>()
const emit = defineEmits<{ discard: []; confirm: [payload: OcrConfirmPayload] }>()

const master = useMasterStore()
const previewUrl = ref<string | null>(null)

const form = reactive({
  transaction_type: props.result.transaction_type === 'income' ? ('income' as const) : ('expense' as const),
  amountYuan: (props.result.amount_cents / 100).toFixed(2),
  transaction_time: props.result.transaction_time,
  account_id: props.result.account_id ?? master.activeAccounts[0]?.id ?? null,
  merchant_text: props.result.merchant ?? '',
  remark: props.result.remark ?? '',
})

const categoryPath = ref<string[] | null>(props.result.category_id ? [props.result.category_id] : null)

const categoryOptions = computed(() => {
  const roots = form.transaction_type === 'income' ? master.incomeCategories : master.expenseCategories
  return roots.map((c) => ({
    value: c.id,
    label: c.name,
    children: (c.children ?? []).map((s) => ({ value: s.id, label: s.name })),
  }))
})

/** need_review 时相关字段高亮（金额/时间为关键字段）。 */
function review(field: 'amount' | 'time'): boolean {
  return props.result.need_review && (field === 'amount' || field === 'time')
}

function confirm(): void {
  const cents = parseYuan(form.amountYuan)
  if (cents === null || cents <= 0) {
    ElMessage.warning('请填写有效金额')
    return
  }
  if (!form.account_id) {
    ElMessage.warning('请选择账户')
    return
  }
  emit('confirm', {
    transaction_type: form.transaction_type,
    amount_cents: cents,
    transaction_time: form.transaction_time,
    account_id: form.account_id,
    category_id: categoryPath.value?.[0] ?? null,
    sub_category_id: categoryPath.value?.[1] ?? null,
    merchant_text: form.merchant_text,
    remark: form.remark,
    tag_ids: props.result.tag_ids ?? [],
    screenshot_id: props.screenshotId ?? '',
  })
}

onMounted(async () => {
  if (!props.screenshotId) return
  try {
    previewUrl.value = await fetchScreenshotUrl(props.screenshotId, 'compressed')
  } catch {
    previewUrl.value = null
  }
})

onBeforeUnmount(() => {
  if (previewUrl.value) URL.revokeObjectURL(previewUrl.value)
})
</script>

<style scoped lang="scss">
.ocr-card {
  &__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
  }

  &__title {
    font-size: 14px;
    font-weight: 600;
  }

  &__warn {
    font-size: 12px;
    color: var(--frozen);
    margin-bottom: 8px;
  }

  &__preview {
    margin: 8px 0;

    img {
      max-width: 100%;
      max-height: 220px;
      border-radius: 8px;
      border: 1px solid var(--border-2);
    }
  }

  &__actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }
}

/* need_review 黄框高亮 */
:deep(.field-review .el-input__wrapper),
:deep(.field-review .el-input__wrapper:hover) {
  box-shadow: 0 0 0 1px #e6a23c inset;
  background: #fdf6ec;
}
</style>
