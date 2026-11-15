<template>
  <el-table :data="rows" size="small" :row-class-name="rowClass" max-height="360" style="width: 100%">
    <el-table-column type="expand">
      <template #default="{ row }">
        <div class="preview-row__detail">
          <div class="preview-row__kv"><span class="text-tertiary">原始行</span>{{ rawText(row) }}</div>
          <div v-if="row.warnings.length" class="preview-row__kv">
            <span class="text-tertiary">提示</span>{{ row.warnings.join('；') }}
          </div>
          <div v-if="row.soft_dup?.is_dup" class="preview-row__kv">
            <span class="text-tertiary">软重复</span>
            {{ row.soft_dup.reason ?? '内容相同' }}
            <template v-if="row.soft_dup.matched_transaction_id">
              （匹配交易 {{ row.soft_dup.matched_transaction_id }}）
            </template>
          </div>
          <div v-if="row.need_confirm.length" class="preview-row__kv">
            <span class="text-tertiary">需确认</span>{{ row.need_confirm.join('；') }}
          </div>
        </div>
      </template>
    </el-table-column>
    <el-table-column label="行" width="58" prop="row_no" />
    <el-table-column label="时间" width="160">
      <template #default="{ row }">
        <el-tooltip v-if="row.parsed && row.warnings.length" :content="row.warnings.join('；')" placement="top">
          <span>{{ row.parsed.transaction_time }}</span>
        </el-tooltip>
        <span v-else-if="row.parsed">{{ row.parsed.transaction_time }}</span>
        <span v-else class="text-tertiary">—</span>
      </template>
    </el-table-column>
    <el-table-column label="类型" width="80">
      <template #default="{ row }">
        <span v-if="row.parsed" :class="row.parsed.transaction_type === 'income' ? 'amount-income' : 'amount-expense'">
          {{ row.parsed.transaction_type === 'income' ? '收入' : '支出' }}
        </span>
        <span v-else class="text-tertiary">—</span>
      </template>
    </el-table-column>
    <el-table-column label="金额" width="120" align="right">
      <template #default="{ row }">
        <span v-if="row.parsed">{{ formatCents(row.parsed.amount_cents) }}</span>
        <span v-else class="text-tertiary">—</span>
      </template>
    </el-table-column>
    <el-table-column label="分类" min-width="120">
      <template #default="{ row }">
        <span v-if="row.parsed?.category_name">
          {{ row.parsed.category_name }}
          <el-tag v-if="!row.parsed.category_id" size="small" type="warning" effect="plain">新建</el-tag>
        </span>
        <span v-else class="text-tertiary">—</span>
      </template>
    </el-table-column>
    <el-table-column label="标签" min-width="110">
      <template #default="{ row }">
        <span v-if="row.parsed?.tag_names?.length">{{ row.parsed.tag_names.join('、') }}</span>
        <span v-else class="text-tertiary">—</span>
      </template>
    </el-table-column>
    <el-table-column label="备注 / 商户" min-width="150">
      <template #default="{ row }">
        <span v-if="row.parsed">{{ row.parsed.merchant_text || row.parsed.remark || '—' }}</span>
        <span v-else class="text-tertiary">—</span>
      </template>
    </el-table-column>
    <el-table-column label="状态" width="130">
      <template #default="{ row }">
        <el-tag v-if="!row.parsed" size="small" type="danger" effect="dark">错误</el-tag>
        <el-tag v-else-if="row.dup?.is_dup" size="small" type="danger" effect="plain">重复</el-tag>
        <el-tag v-else-if="row.soft_dup?.is_dup" size="small" type="warning" effect="plain">软重复</el-tag>
        <el-tag v-else-if="row.frozen" size="small" type="warning" effect="dark">冻结区</el-tag>
        <el-tag v-else size="small" type="success" effect="plain">可导入</el-tag>
      </template>
    </el-table-column>
  </el-table>
</template>

<script setup lang="ts">
/**
 * 步 4 的行表（ARCHITECTURE-V12 §5.1 / T06 验收 ③）：
 * 错误行红底、软重复黄底、冻结橙底，展开可看原始行与原因。
 */
import { formatCents } from '@/utils/money'
import type { SheetPreviewRow } from '@/api/types'

defineProps<{ rows: SheetPreviewRow[] }>()

function rowClass({ row }: { row: SheetPreviewRow }): string {
  if (!row.parsed) return 'sheet-row--error'
  if (row.dup?.is_dup) return 'sheet-row--error'
  if (row.soft_dup?.is_dup) return 'sheet-row--softdup'
  if (row.frozen) return 'sheet-row--frozen'
  return ''
}

function rawText(row: SheetPreviewRow): string {
  return Object.entries(row.raw ?? {})
    .map(([k, v]) => `${k}=${v ?? ''}`)
    .join(' · ')
}
</script>

<style lang="scss">
/* 行级配色需非 scoped（el-table 行渲染在内部） */
.sheet-row--error {
  --el-table-tr-bg-color: rgba(235, 87, 87, 0.12);
}

.sheet-row--softdup {
  --el-table-tr-bg-color: rgba(242, 153, 74, 0.14);
}

.sheet-row--frozen {
  --el-table-tr-bg-color: rgba(242, 153, 74, 0.08);
  box-shadow: inset 3px 0 0 var(--frozen);
}
</style>

<style scoped lang="scss">
.preview-row {
  &__detail {
    padding: 4px 16px 8px;
    font-size: 12px;
    line-height: 20px;
    color: var(--text-2);
  }

  &__kv {
    display: flex;
    gap: 8px;
  }
}
</style>
