<template>
  <div v-if="preview" class="preview-step">
    <div class="preview-step__summary">
      <el-tag size="small" type="info" effect="plain">总行数 {{ preview.summary.total_rows }}</el-tag>
      <el-tag size="small" type="success" effect="plain">可导入 {{ preview.summary.parsed_ok }}</el-tag>
      <el-tag size="small" type="danger" effect="plain">错误 {{ preview.summary.error }}</el-tag>
      <el-tag size="small" type="warning" effect="plain">跳过 {{ preview.summary.skipped }}</el-tag>
      <el-tag size="small" type="danger" effect="plain">重复 {{ preview.summary.dup_count }}</el-tag>
      <el-tag size="small" type="warning" effect="plain">软重复 {{ preview.summary.soft_dup_count }}</el-tag>
      <el-tag size="small" type="warning" effect="dark">冻结区 {{ preview.summary.frozen_count }}</el-tag>
    </div>

    <!-- v1.2 R6：冻结策略（决策上下文在第 4 步最完整，故放这里而非步 2/3） -->
    <div class="preview-step__freeze">
      <div class="preview-step__freeze-head">
        <span class="preview-step__freeze-label">历史数据处理</span>
        <el-radio-group v-model="state.freezeMode" size="small">
          <el-radio v-for="opt in FREEZE_MODE_OPTIONS" :key="opt.value" :value="opt.value">
            {{ opt.label }}
          </el-radio>
        </el-radio-group>
        <el-tooltip :content="activeDescription" placement="top">
          <el-icon class="preview-step__freeze-help"><QuestionFilled /></el-icon>
        </el-tooltip>
      </div>
      <div class="preview-step__freeze-hint" :class="{ 'preview-step__freeze-hint--danger': state.freezeMode === 'immediate' }">
        {{ freezeHint }}
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 8px"
      :title="`已映射 ${configKeys.length} 张表；预览最多展示 200 行${rowsCapped ? '（已截断）' : ''}，提交时按全量落盘数据入库`"
    />

    <el-tabs v-model="activeSheet">
      <el-tab-pane v-for="sheet in preview.sheets" :key="sheetKey(sheet)" :name="sheetKey(sheet)">
        <template #label>
          <span class="preview-step__tab">{{ sheetName(sheet) }}</span>
          <el-tag v-if="sheet.error" size="small" type="danger" effect="plain" style="margin-left: 4px">
            {{ sheet.error }}
          </el-tag>
        </template>

        <div class="preview-step__sheet-head">
          <span>解析成功 <b>{{ sheet.parsed_ok }}</b> / 共 {{ sheet.total_rows }} 行</span>
          <span>支出 {{ sheet.direction_stats.expense }} · 收入 {{ sheet.direction_stats.income }}</span>
          <span v-if="sheet.date_range.length === 2">
            日期范围 {{ sheet.date_range[0] }} ~ {{ sheet.date_range[1] }}
          </span>
        </div>

        <SheetPreviewTable :rows="rowsOf(sheet.sheet_index)" />

        <div v-if="sheet.errors.length" class="preview-step__errors">
          <div class="preview-step__section-title">错误行</div>
          <div v-for="(err, i) in sheet.errors" :key="i" class="preview-step__error-item">
            第 {{ err.row_no }} 行 · 列「{{ err.column || '—' }}」· 值「{{ err.value }}」· {{ err.reason }}
          </div>
        </div>

        <template v-if="sheet.time_distribution_sample.length">
          <div class="preview-step__section-title">
            同日时间分布样例
            <el-tooltip content="源文件无时间，已按当日顺序平均分布" placement="top">
              <el-icon style="margin-left: 4px; vertical-align: -2px"><QuestionFilled /></el-icon>
            </el-tooltip>
          </div>
          <div v-for="s in sheet.time_distribution_sample" :key="s.local_date" class="preview-step__dist">
            <span class="text-secondary">{{ s.local_date }}</span>
            <span class="text-tertiary">（{{ s.count }} 笔）</span>
            <span>{{ s.times.join(' / ') }}</span>
          </div>
        </template>
      </el-tab-pane>
    </el-tabs>

    <div class="preview-step__auto">
      <div class="preview-step__section-title">将自动新建的分类 / 标签</div>
      <div class="preview-step__chips">
        <template v-if="newCategories.length">
          <el-tag v-for="c in newCategories" :key="`${c.name}-${c.transaction_type}`" size="small" type="warning" effect="plain">
            分类：{{ c.name }}（{{ c.rows }} 行，{{ c.transaction_type === 'income' ? '收入' : '支出' }}）
          </el-tag>
        </template>
        <span v-else class="text-tertiary">无（全部命中已有分类）</span>
      </div>
      <div class="preview-step__chips" style="margin-top: 6px">
        <template v-if="newTags.length">
          <el-tag v-for="t in newTags" :key="t.name" size="small" type="warning" effect="plain">
            标签：{{ t.name }}（{{ t.rows }} 行）
          </el-tag>
        </template>
        <span v-else class="text-tertiary">无新增标签</span>
      </div>
      <div v-if="existingChips.length" class="preview-step__chips" style="margin-top: 6px">
        <el-tag v-for="t in existingChips" :key="t" size="small" type="success" effect="plain">{{ t }}</el-tag>
      </div>
    </div>

    <div class="preview-step__options">
      <el-checkbox v-model="state.options.createMissingCategories">自动新建缺失分类</el-checkbox>
      <el-checkbox v-model="state.options.createMissingTags">自动新建缺失标签</el-checkbox>
      <el-checkbox v-model="state.options.skipDuplicates">跳过重复行（同批次/同指纹）</el-checkbox>
      <el-checkbox v-model="state.options.skipSoftDuplicates">跳过软重复（内容相同）</el-checkbox>
      <el-checkbox v-model="state.options.allowFrozen">允许写入冻结区</el-checkbox>
      <el-tooltip content="软重复可能是真实的两笔，默认不跳过" placement="top">
        <el-icon><QuestionFilled /></el-icon>
      </el-tooltip>
    </div>

    <el-alert
      v-if="!state.options.allowFrozen && preview.summary.frozen_count > 0"
      type="warning"
      show-icon
      :closable="false"
      :title="`本次预览含 ${preview.summary.frozen_count} 笔冻结区交易，未勾选「允许写入冻结区」时提交会被 409 FROZEN 拒绝`"
    />
  </div>
</template>

<script setup lang="ts">
/**
 * 步 4：预览校验（ARCHITECTURE-V12 §5.1）：
 * 汇总条 + 表级 Tab + 行表（错误红 / 软重复黄 / 冻结橙）+ 自动新增分类标签 + 时间分布样例 + 提交选项。
 */
import { computed, ref, watch } from 'vue'
import { QuestionFilled } from '@element-plus/icons-vue'
import SheetPreviewTable from './SheetPreviewTable.vue'
import type { SheetPreviewRow, SheetPreviewSheetResult } from '@/api/types'
import { FREEZE_MODE_OPTIONS, freezePolicyHint } from '@/utils/freeze'
import type { WizardState } from './types'

const props = defineProps<{ state: WizardState }>()

const preview = computed(() => props.state.preview)
const configKeys = computed(() => props.state.configs.map((c) => c.key))

const activeSheet = ref('')
watch(
  preview,
  (p) => {
    if (p && p.sheets.length) activeSheet.value = sheetKey(p.sheets[0])
    else activeSheet.value = ''
  },
  { immediate: true },
)

function sheetKey(sheet: SheetPreviewSheetResult): string {
  return `${sheet.file_id}:${sheet.sheet_index}`
}

function sheetName(sheet: SheetPreviewSheetResult): string {
  const cfg = props.state.configs.find((c) => c.fileId === sheet.file_id && c.sheetIndex === sheet.sheet_index)
  return cfg ? `${cfg.fileName} / ${cfg.sheetName}` : `表 ${sheet.sheet_index}`
}

function rowsOf(sheetIndex: number): SheetPreviewRow[] {
  return (preview.value?.rows ?? [])
    .filter((r) => r.sheet_index === sheetIndex)
    .slice(0, MAX_PREVIEW_ROWS)
}

/** 冻结策略实时说明（切换时文案同步变，T13 验收 ①）。 */
const freezeHint = computed(() => freezePolicyHint(props.state.freezeMode, frozenCount.value))
const activeDescription = computed(
  () => FREEZE_MODE_OPTIONS.find((o) => o.value === props.state.freezeMode)?.description ?? '',
)
/** 早于冻结天数、会进冻结区的行数（预览 summary.frozen_count）。 */
const frozenCount = computed(() => preview.value?.summary.frozen_count ?? 0)

/** 后端已按 200 行截断（rows_truncated），这里再兜一层，避免异常响应把整张表塞进 DOM。 */
const MAX_PREVIEW_ROWS = 200
const rowsCapped = computed(
  () => Boolean(preview.value?.rows_truncated) || (preview.value?.rows.length ?? 0) > MAX_PREVIEW_ROWS,
)

const newCategories = computed(() => (preview.value?.auto_create.categories ?? []).filter((c) => !c.exists))
const newTags = computed(() => (preview.value?.auto_create.tags ?? []).filter((t) => !t.exists))
const existingChips = computed(() => [
  ...(preview.value?.auto_create.categories ?? []).filter((c) => c.exists).map((c) => `已有分类：${c.name}`),
  ...(preview.value?.auto_create.tags ?? []).filter((t) => t.exists).map((t) => `已有标签：${t.name}`),
])
</script>

<style scoped lang="scss">
.preview-step {
  &__summary {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 8px;
  }

  &__tab {
    font-size: 12px;
  }

  &__sheet-head {
    display: flex;
    gap: 16px;
    flex-wrap: wrap;
    font-size: 12px;
    color: var(--text-2);
    margin-bottom: 8px;
  }

  &__section-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--text-1);
    margin: 12px 0 6px;
  }

  &__errors {
    margin-top: 8px;
  }

  &__error-item {
    font-size: 12px;
    line-height: 20px;
    color: var(--expense);
  }

  &__dist {
    font-size: 12px;
    line-height: 20px;
    display: flex;
    gap: 8px;
  }

  &__auto {
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px dashed var(--border-1);
  }

  &__chips {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }

  &__freeze {
    border: 1px solid var(--border-2);
    border-radius: var(--radius-card);
    padding: 10px 12px;
    margin-bottom: 8px;
    background: var(--bg-page);
  }

  &__freeze-head {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  &__freeze-label {
    font-size: 13px;
    font-weight: 600;
    color: var(--text-1);
  }

  &__freeze-help {
    color: var(--text-3);
    cursor: help;
  }

  &__freeze-hint {
    margin-top: 4px;
    font-size: 12px;
    color: var(--text-2);

    &--danger {
      color: var(--frozen);
    }
  }

  &__options {
    display: flex;
    align-items: center;
    gap: 16px;
    flex-wrap: wrap;
    margin: 12px 0 8px;
  }
}
</style>
