<template>
  <div class="import-view">
    <PageHeader title="导入中心" subtitle="CSV 账单（微信/支付宝）、Excel / CSV 账本、截图 OCR" />
    <div class="card">
      <el-tabs v-model="tab">
        <el-tab-pane label="CSV 导入" name="csv">
          <CsvImportPanel v-if="mountedTabs.csv" @view-created="onViewCreated" />
        </el-tab-pane>
        <el-tab-pane label="Excel / CSV 账本导入" name="sheet">
          <div v-if="mountedTabs.sheet" class="sheet-entry">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              title="支持一次上传多个 Excel / CSV 账本文件，自动识别表头与列含义；汇总表会被建议跳过，导入前可预览每一行"
              style="margin-bottom: 12px"
            />
            <SheetImportWizard />
          </div>
        </el-tab-pane>
        <el-tab-pane label="截图识别（OCR）" name="ocr">
          <OcrUploadPanel
            v-if="mountedTabs.ocr"
            @manual-fallback="quickEntryOpen = true"
            @confirmed="onOcrConfirmed"
          />
        </el-tab-pane>
      </el-tabs>
    </div>

    <!-- OCR 确认后预填 TransactionForm（需用户再次保存） -->
    <el-dialog
      v-model="formOpen"
      title="确认录入（OCR 预填）"
      width="min(880px, 94vw)"
      class="dlg--wide"
      :close-on-click-modal="false"
    >
      <TransactionForm
        v-if="formOpen"
        ref="formRef"
        :defaults="ocrDefaults"
        @saved="onFormSaved"
        @frozen-conflict="onFrozenConflict"
      />
      <template #footer>
        <el-button @click="formOpen = false">取消</el-button>
        <el-button type="primary" @click="formRef?.submit()">保存</el-button>
      </template>
    </el-dialog>

    <QuickEntryDialog v-model="quickEntryOpen" />
  </div>
</template>

<script setup lang="ts">
/** 导入中心（T15 / v1.2 T06）：CSV 两阶段导入 + Excel/CSV 账本向导 + OCR 上传轮询。 */
import { nextTick, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import type { ApiError } from '@/api/client'
import type { OcrConfirmPayload, TransactionCreateResult } from '@/api/types'
import PageHeader from '@/components/common/PageHeader.vue'
import CsvImportPanel from '@/components/import/CsvImportPanel.vue'
import SheetImportWizard from '@/components/import/sheet/SheetImportWizard.vue'
import OcrUploadPanel from '@/components/import/OcrUploadPanel.vue'
import TransactionForm from '@/components/entry/TransactionForm.vue'
import QuickEntryDialog from '@/components/entry/QuickEntryDialog.vue'

const router = useRouter()
// 默认落在 v1.2 主功能「Excel / CSV 账本导入」；CSV 与 OCR 两个旧入口保留（按需挂载）
const tab = ref<'csv' | 'sheet' | 'ocr'>('sheet')
const mountedTabs = reactive({ csv: false, sheet: true, ocr: false })
const formOpen = ref(false)
const quickEntryOpen = ref(false)
const formRef = ref<InstanceType<typeof TransactionForm>>()
const ocrDefaults = reactive({
  accountId: null as string | null,
  categoryId: null as string | null,
  subCategoryId: null as string | null,
  merchantId: null as string | null,
})
const pendingPayload = ref<OcrConfirmPayload | null>(null)

// 两个 tab 均按需挂载，切过一次后保活
watch(tab, (t) => {
  mountedTabs[t] = true
})

/** CSV 提交成功 → 跳转明细页查看新交易。 */
function onViewCreated(ids: string[]): void {
  if (!ids.length) return
  ElMessage.success(`已创建 ${ids.length} 笔交易，正在跳转明细页`)
  void router.push('/transactions')
}

/** OCR 确认 → 打开预填表单。 */
function onOcrConfirmed(payload: OcrConfirmPayload): void {
  pendingPayload.value = payload
  ocrDefaults.accountId = payload.account_id
  ocrDefaults.categoryId = payload.category_id
  ocrDefaults.subCategoryId = payload.sub_category_id
  ocrDefaults.merchantId = null
  formOpen.value = true
  nextTick(() => {
    formRef.value?.fill({
      transaction_type: payload.transaction_type === 'income' ? 'income' : 'expense',
      amountYuan: (payload.amount_cents / 100).toFixed(2),
      transaction_time: payload.transaction_time,
      remark: payload.remark,
      tag_ids: payload.tag_ids,
    })
    formRef.value?.focusAmount()
  })
}

function onFormSaved(result: TransactionCreateResult | null): void {
  formOpen.value = false
  if (result) ElMessage.success('已保存（来源：截图识别）')
  pendingPayload.value = null
}

function onFrozenConflict(err: ApiError): void {
  ElMessage.warning(err.message || '该笔已进入冻结区，不可直接修改')
  formOpen.value = false
}
</script>
