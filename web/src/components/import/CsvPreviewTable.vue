<template>
  <el-table :data="rows" size="small" row-key="row_no" max-height="440" style="width: 100%" :row-class-name="rowClass">
    <el-table-column width="44" align="center">
      <template #default="{ row }">
        <el-checkbox
          :model-value="selected.includes(row.row_no)"
          :disabled="!row.parsed"
          @update:model-value="(v: boolean) => toggle(row.row_no, v)"
        />
      </template>
    </el-table-column>
    <el-table-column type="expand">
      <template #default="{ row }">
        <div class="preview-expand">
          <div v-if="!row.parsed" class="text-tertiary">该行未能解析，原因：{{ row.warnings.join('；') || '未知' }}</div>
          <div v-else>
            <div v-for="(v, k) in row.raw" :key="k" class="preview-expand__raw">
              <span class="text-tertiary">{{ k }}：</span>{{ v }}
            </div>
          </div>
          <div v-if="row.dup?.is_dup" class="text-tertiary">
            重复原因：{{ row.dup.reason }}（匹配交易 {{ row.dup.matched_transaction_id ?? '未知' }}）
          </div>
          <div v-for="w in row.warnings" :key="w" class="text-tertiary">⚠ {{ w }}</div>
        </div>
      </template>
    </el-table-column>
    <el-table-column label="行" width="56" prop="row_no" />
    <el-table-column label="时间" width="150">
      <template #default="{ row }">{{ row.parsed?.transaction_time ?? '—' }}</template>
    </el-table-column>
    <el-table-column label="类型" width="110">
      <template #default="{ row }">
        <el-select
          v-if="row.parsed"
          :model-value="ov(row).transaction_type ?? row.parsed.transaction_type"
          size="small"
          @update:model-value="(v: TransactionType) => patch(row.row_no, { transaction_type: v })"
        >
          <el-option value="expense" label="支出" />
          <el-option value="income" label="收入" />
        </el-select>
      </template>
    </el-table-column>
    <el-table-column label="金额(元)" width="130">
      <template #default="{ row }">
        <el-input
          v-if="row.parsed"
          :model-value="amountYuan(row)"
          size="small"
          @update:model-value="(v: string) => onAmount(row, v)"
        />
      </template>
    </el-table-column>
    <el-table-column label="分类" min-width="150">
      <template #default="{ row }">
        <el-select
          v-if="row.parsed"
          :model-value="ov(row).category_id ?? ''"
          size="small"
          clearable
          placeholder="使用默认"
          @update:model-value="(v: string) => patch(row.row_no, { category_id: v || undefined })"
        >
          <el-option v-for="c in flatCategories" :key="c.id" :value="c.id" :label="c.name" />
        </el-select>
      </template>
    </el-table-column>
    <el-table-column label="商户" width="150">
      <template #default="{ row }">
        <el-input
          v-if="row.parsed"
          :model-value="ov(row).merchant_text ?? row.parsed.merchant_text"
          size="small"
          @update:model-value="(v: string) => patch(row.row_no, { merchant_text: v })"
        />
      </template>
    </el-table-column>
    <el-table-column label="备注" min-width="150">
      <template #default="{ row }">
        <el-input
          v-if="row.parsed"
          :model-value="ov(row).remark ?? row.parsed.remark"
          size="small"
          @update:model-value="(v: string) => patch(row.row_no, { remark: v })"
        />
      </template>
    </el-table-column>
    <el-table-column label="状态" width="110">
      <template #default="{ row }">
        <el-tooltip v-if="row.dup?.is_dup" :content="`重复：${row.dup.reason}（${row.dup.matched_transaction_id ?? ''}）`">
          <el-tag size="small" type="warning">疑似重复</el-tag>
        </el-tooltip>
        <el-tag v-if="row.frozen" size="small" type="danger" style="margin-left: 4px">冻结区</el-tag>
        <el-tag v-if="!row.parsed" size="small" type="info" style="margin-left: 4px">跳过</el-tag>
      </template>
    </el-table-column>
    <el-table-column label="需确认" width="90">
      <template #default="{ row }">
        <el-tag v-for="f in row.need_confirm" :key="f" size="small" type="warning" effect="plain">{{ f }}</el-tag>
      </template>
    </el-table-column>
  </el-table>
</template>

<script setup lang="ts">
/**
 * CSV 可编辑预览表（T15 验收 1）：dup 行黄底、frozen 行橙底、skipped 行可展开看原因、
 * need_confirm 字段高亮；修改以 override 形式回传父级。
 */
import { computed } from 'vue'
import { useMasterStore } from '@/stores/master'
import { parseYuan } from '@/utils/money'
import type { CsvPreviewRow, CsvRowOverride, TransactionType } from '@/api/types'

const props = defineProps<{
  rows: CsvPreviewRow[]
  selected: number[]
  overrides: Record<number, CsvRowOverride>
}>()

const emit = defineEmits<{
  'update:selected': [v: number[]]
  'update:overrides': [v: Record<number, CsvRowOverride>]
}>()

const master = useMasterStore()
const flatCategories = computed(() => master.flatCategories)

function toggle(rowNo: number, checked: boolean): void {
  const next = checked ? [...props.selected, rowNo] : props.selected.filter((n) => n !== rowNo)
  emit('update:selected', next)
}

function ov(row: CsvPreviewRow): CsvRowOverride {
  return props.overrides[row.row_no] ?? {}
}

function amountYuan(row: CsvPreviewRow): string {
  const cents = ov(row).amount_cents ?? row.parsed?.amount_cents ?? 0
  return (cents / 100).toFixed(2)
}

function onAmount(row: CsvPreviewRow, v: string): void {
  const cents = parseYuan(v)
  patch(row.row_no, { amount_cents: cents ?? undefined })
}

function patch(rowNo: number, p: CsvRowOverride): void {
  const next = { ...props.overrides, [rowNo]: { ...(props.overrides[rowNo] ?? {}), ...p } }
  emit('update:overrides', next)
}

function rowClass({ row }: { row: CsvPreviewRow }): string {
  const cls: string[] = []
  if (row.dup?.is_dup) cls.push('dup-row')
  if (row.frozen) cls.push('frozen-row')
  return cls.join(' ')
}
</script>

<style scoped lang="scss">
.preview-expand {
  padding: 4px 12px;
  font-size: 12px;

  &__raw {
    line-height: 1.8;
  }
}
</style>

<style lang="scss">
/* dup 行黄底 / frozen 行橙底（el-table 行渲染在组件外，需非 scoped） */
.el-table .dup-row td {
  background: #fff8e1 !important;
}

html.dark .el-table .dup-row td {
  background: #3a2f14 !important;
}

.el-table .frozen-row td {
  background: #fff1e6 !important;
}

html.dark .el-table .frozen-row td {
  background: #3a2415 !important;
}
</style>
