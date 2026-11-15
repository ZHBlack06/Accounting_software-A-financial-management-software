<template>
  <div class="csv-panel">
    <!-- 上传区 -->
    <div
      class="csv-panel__drop"
      :class="{ 'csv-panel__drop--active': dragging }"
      @dragover.prevent="dragging = true"
      @dragleave.prevent="dragging = false"
      @drop.prevent="onDrop"
      @click="pickFile"
    >
      <el-icon :size="28"><UploadFilled /></el-icon>
      <div class="csv-panel__drop-title">拖拽微信/支付宝 CSV 账单到此处，或点击选择文件</div>
      <div class="text-tertiary">支持 UTF-8 BOM / GBK；单文件 ≤ 20MB</div>
      <input ref="fileInput" type="file" accept=".csv,text/csv" style="display: none" @change="onFileChange" />
    </div>

    <div class="csv-panel__controls">
      <el-select v-model="accountId" placeholder="默认账户（必选）" style="width: 200px">
        <el-option v-for="a in master.activeAccounts" :key="a.id" :value="a.id" :label="a.display_name" />
      </el-select>
      <el-select v-model="source" style="width: 130px">
        <el-option value="auto" label="自动识别" />
        <el-option value="wechat" label="微信" />
        <el-option value="alipay" label="支付宝" />
      </el-select>
      <el-checkbox v-model="allowFrozen">允许写入冻结区</el-checkbox>
    </div>

    <el-alert
      v-if="preview && preview.summary.frozen_count > 0"
      type="warning"
      show-icon
      :closable="false"
      style="margin: 8px 0"
      :title="`本次预览含 ${preview.summary.frozen_count} 笔冻结区交易，写入后不可直接修改（需调整分录）`"
    />

    <template v-if="preview">
      <div class="csv-panel__summary">
        <el-tag size="small" type="success">来源：{{ preview.source === 'wechat' ? '微信' : '支付宝' }}</el-tag>
        <el-tag size="small" type="info">编码：{{ preview.encoding }}</el-tag>
        <span>共 {{ preview.summary.total_rows }} 行</span>
        <span>解析成功 <b>{{ preview.summary.parsed_ok }}</b></span>
        <span>跳过 {{ preview.summary.skipped }}</span>
        <span>重复 {{ preview.summary.dup_count }}</span>
        <span>冻结 {{ preview.summary.frozen_count }}</span>
        <span class="text-tertiary">已选 {{ selected.length }} 行</span>
      </div>

      <div class="csv-panel__actions">
        <el-button size="small" @click="selectAll">全选</el-button>
        <el-button size="small" @click="invert">反选</el-button>
        <el-button size="small" @click="onlyNonDup">仅选非重复</el-button>
        <el-button size="small" @click="onlyNonFrozen">仅选非冻结</el-button>
        <el-button type="primary" size="small" :loading="committing" @click="confirmCommit">提交导入</el-button>
        <el-button size="small" text @click="reset">重新上传</el-button>
      </div>

      <CsvPreviewTable
        :rows="preview.rows"
        :selected="selected"
        :overrides="overrides"
        @update:selected="selected = $event"
        @update:overrides="overrides = $event"
      />

      <el-alert
        v-if="result"
        type="success"
        show-icon
        :closable="false"
        style="margin-top: 12px"
        :title="`导入完成：创建 ${result.created} 笔，跳过重复 ${result.skipped_dup} 笔，无效 ${result.skipped_invalid} 笔，含冻结 ${result.frozen_created} 笔`"
      >
        <el-button size="small" type="primary" @click="emit('viewCreated', result.transaction_ids)">查看新交易</el-button>
      </el-alert>
    </template>

    <el-alert v-else-if="error" type="error" show-icon :closable="false" :title="`预览失败：${error}`" style="margin-top: 8px">
      <el-button size="small" @click="reset">重新上传</el-button>
    </el-alert>
    <div v-else-if="loading" v-loading="true" class="csv-panel__loading" />
  </div>
</template>

<script setup lang="ts">
/**
 * CSV 导入面板（T15 验收 1–4）：拖拽上传 → source/encoding/summary → 可编辑预览 →
 * 全选/仅选非重复/仅选非冻结 → 确认框（创建 N 笔，跳过重复 M 笔，含冻结 K 笔）→ 结果。
 * 含冻结行且未勾选「允许写入冻结区」时前端直接拦截。
 */
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { UploadFilled } from '@element-plus/icons-vue'
import { commitCsv, previewCsv } from '@/api/imports'
import { useMasterStore } from '@/stores/master'
import { errorText } from '@/api/client'
import type { CsvCommitResult, CsvPreviewDTO, CsvRowOverride } from '@/api/types'
import CsvPreviewTable from './CsvPreviewTable.vue'

const emit = defineEmits<{ viewCreated: [ids: string[]] }>()

const master = useMasterStore()
const fileInput = ref<HTMLInputElement>()
const dragging = ref(false)
const loading = ref(false)
const committing = ref(false)
const error = ref<string | null>(null)
const preview = ref<CsvPreviewDTO | null>(null)
const selected = ref<number[]>([])
const overrides = ref<Record<number, CsvRowOverride>>({})
const accountId = ref<string>(master.activeAccounts[0]?.id ?? '')
const source = ref<'auto' | 'wechat' | 'alipay'>('auto')
const allowFrozen = ref(false)
const result = ref<CsvCommitResult | null>(null)

const selectedRows = computed(() => preview.value?.rows.filter((r) => selected.value.includes(r.row_no)) ?? [])
const selectedFrozen = computed(() => selectedRows.value.filter((r) => r.frozen))
const selectedDup = computed(() => selectedRows.value.filter((r) => r.dup?.is_dup))

function pickFile(): void {
  fileInput.value?.click()
}

function onFileChange(e: Event): void {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (file) void upload(file)
}

function onDrop(e: DragEvent): void {
  dragging.value = false
  const file = e.dataTransfer?.files?.[0]
  if (file) void upload(file)
}

async function upload(file: File): Promise<void> {
  if (!file.name.toLowerCase().endsWith('.csv')) {
    ElMessage.warning('请上传 .csv 文件')
    return
  }
  if (file.size > 20 * 1024 * 1024) {
    ElMessage.warning('文件不得超过 20MB')
    return
  }
  loading.value = true
  error.value = null
  result.value = null
  try {
    const res = await previewCsv(file, accountId.value, source.value)
    preview.value = res
    overrides.value = {}
    if (!accountId.value && res.suggest_defaults.account_id) accountId.value = res.suggest_defaults.account_id
    selected.value = res.rows
      .filter((r) => r.parsed && !r.dup?.is_dup && !r.frozen)
      .map((r) => r.row_no)
  } catch (e) {
    preview.value = null
    error.value = errorText(e)
  } finally {
    loading.value = false
  }
}

function selectAll(): void {
  selected.value = preview.value?.rows.filter((r) => r.parsed).map((r) => r.row_no) ?? []
}

function invert(): void {
  const all = preview.value?.rows.filter((r) => r.parsed).map((r) => r.row_no) ?? []
  selected.value = all.filter((n) => !selected.value.includes(n))
}

function onlyNonDup(): void {
  selected.value = (preview.value?.rows ?? []).filter((r) => r.parsed && !r.dup?.is_dup).map((r) => r.row_no)
}

function onlyNonFrozen(): void {
  selected.value = (preview.value?.rows ?? []).filter((r) => r.parsed && !r.frozen).map((r) => r.row_no)
}

function reset(): void {
  preview.value = null
  selected.value = []
  overrides.value = {}
  result.value = null
  error.value = null
  if (fileInput.value) fileInput.value.value = ''
}

async function confirmCommit(): Promise<void> {
  if (!preview.value) return
  if (!accountId.value) {
    ElMessage.warning('请选择默认账户')
    return
  }
  if (!selected.value.length) {
    ElMessage.warning('请至少选择一行')
    return
  }
  // 含冻结行未勾选「允许写入冻结区」→ 前端拦截
  if (selectedFrozen.value.length > 0 && !allowFrozen.value) {
    ElMessage.warning(`所选行含 ${selectedFrozen.value.length} 笔冻结区交易，请勾选「允许写入冻结区」后重试`)
    return
  }
  const msg = `将创建 ${selected.value.length} 笔，跳过重复 ${selectedDup.value.length} 笔，含冻结 ${selectedFrozen.value.length} 笔。确认导入？`
  try {
    await ElMessageBox.confirm(msg, '确认导入', { confirmButtonText: '确认导入', cancelButtonText: '再检查', type: 'warning' })
  } catch {
    return
  }
  committing.value = true
  try {
    result.value = await commitCsv({
      batch_id: preview.value.batch_id,
      row_nos: selected.value,
      defaults: {
        account_id: accountId.value,
        category_id: preview.value.suggest_defaults.category_id,
        tag_ids: [],
      },
      overrides: Object.entries(overrides.value).map(([rowNo, o]) => ({ row_no: Number(rowNo), ...o })),
      skip_duplicates: true,
      allow_frozen: allowFrozen.value,
    })
    ElMessage.success(`导入完成，创建 ${result.value.created} 笔`)
    await master.loadAccounts()
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    committing.value = false
  }
}

onMounted(async () => {
  if (!master.loaded.accounts) await master.loadAccounts()
  if (!accountId.value) accountId.value = master.activeAccounts[0]?.id ?? ''
})
</script>

<style scoped lang="scss">
.csv-panel {
  &__drop {
    border: 2px dashed var(--border-1);
    border-radius: 12px;
    padding: 28px;
    text-align: center;
    cursor: pointer;
    color: var(--text-2);
    transition: border-color 0.2s, background 0.2s;

    &:hover,
    &--active {
      border-color: var(--accent);
      background: var(--accent-bg);
    }
  }

  &__drop-title {
    margin: 8px 0 4px;
    font-size: 14px;
    color: var(--text-1);
  }

  &__controls {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 12px;
    flex-wrap: wrap;
  }

  &__summary {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    font-size: 13px;
    color: var(--text-2);
    padding: 8px 0;
  }

  &__actions {
    display: flex;
    gap: 8px;
    align-items: center;
    margin-bottom: 8px;
    flex-wrap: wrap;
  }

  &__loading {
    height: 120px;
    margin-top: 12px;
  }
}
</style>
