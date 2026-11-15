<template>
  <div class="sheet-picker">
    <div class="sheet-picker__toolbar">
      <el-button size="small" @click="setAll(true)">全选可导入表单</el-button>
      <el-button size="small" @click="setAll(false)">全部取消</el-button>
      <span class="text-tertiary">
        共 {{ state.candidates.length }} 张表，已勾选 {{ selectedCount }} 张
      </span>
    </div>

    <div v-for="group in groups" :key="group.fileId" class="sheet-picker__file">
      <div class="sheet-picker__file-head">
        <el-icon><Document /></el-icon>
        <span class="sheet-picker__file-name">{{ group.fileName }}</span>
        <el-tag size="small" type="info" effect="plain">{{ group.sheets.length }} 张表</el-tag>
      </div>

      <div
        v-for="sheet in group.sheets"
        :key="sheet.sheetIndex"
        class="sheet-picker__row"
        :class="{ 'sheet-picker__row--skipped': !sheet.selected }"
      >
        <div class="sheet-picker__row-main">
          <el-checkbox v-model="sheet.selected" />
          <span class="sheet-picker__name">{{ sheet.sheetName }}</span>
          <el-tag v-if="sheet.rowCount < 0" size="small" type="warning" effect="plain">行数未知（超大表）</el-tag>
          <span v-else class="text-tertiary">{{ sheet.rowCount }} 行 · {{ sheet.colCount }} 列</span>
          <el-tag v-if="sheet.suggestedSkip" size="small" type="warning" effect="plain">疑似汇总表，建议不导入</el-tag>
          <span v-if="sheet.suggestedSkip && sheet.skipReason" class="text-tertiary">{{ sheet.skipReason }}</span>
          <span class="sheet-picker__spacer" />
          <span class="text-tertiary">表头行</span>
          <el-select v-model="sheet.headerRow" size="small" style="width: 88px">
            <el-option v-for="h in sheet.headerRowCandidates" :key="h" :value="h" :label="String(h)" />
          </el-select>
          <el-collapse-transition>
            <el-button link size="small" @click="toggle(group.fileId, sheet.sheetIndex)">
              {{ isOpen(group.fileId, sheet.sheetIndex) ? '收起预览' : '展开预览' }}
            </el-button>
          </el-collapse-transition>
        </div>

        <div v-if="isOpen(group.fileId, sheet.sheetIndex)" class="sheet-picker__sample">
          <div class="text-tertiary">列名：{{ sheet.columns.join(' | ') || '（未识别到表头）' }}</div>
          <el-table :data="sampleRows(sheet)" size="small" border style="width: 100%; margin-top: 6px">
            <el-table-column
              v-for="(col, ci) in sheet.columns"
              :key="ci"
              :label="col || `列${ci + 1}`"
              min-width="96"
            >
              <template #default="{ row }">{{ row[ci] ?? '' }}</template>
            </el-table-column>
          </el-table>
        </div>
      </div>
    </div>

    <el-alert
      v-if="state.probe?.warnings?.length"
      type="warning"
      show-icon
      :closable="false"
      style="margin-top: 8px"
      title="探测提示"
    >
      <div v-for="(w, i) in state.probe.warnings" :key="i" class="sheet-picker__warn">{{ w }}</div>
    </el-alert>
  </div>
</template>

<script setup lang="ts">
/**
 * 步 2：选择工作表（ARCHITECTURE-V12 §5.1）。
 * - 按文件分组；每张表可改表头行、展开样本预览；
 * - `suggested_skip` 的表默认不勾选并展示原因（汇总表建议跳过）。
 */
import { computed, ref } from 'vue'
import { Document } from '@element-plus/icons-vue'
import type { SheetCandidate, WizardState } from './types'
import { groupByFile } from './types'

const props = defineProps<{ state: WizardState }>()

const openKeys = ref<Set<string>>(new Set())

const groups = computed(() => groupByFile(props.state.candidates))
const selectedCount = computed(() => props.state.candidates.filter((c) => c.selected).length)

function key(fileId: string, sheetIndex: number): string {
  return `${fileId}:${sheetIndex}`
}

function isOpen(fileId: string, sheetIndex: number): boolean {
  return openKeys.value.has(key(fileId, sheetIndex))
}

function toggle(fileId: string, sheetIndex: number): void {
  const k = key(fileId, sheetIndex)
  const next = new Set(openKeys.value)
  if (next.has(k)) next.delete(k)
  else next.add(k)
  openKeys.value = next
}

/** 样本行转成列索引对象，供 el-table 动态列渲染。 */
function sampleRows(sheet: SheetCandidate): Record<number, string>[] {
  return (sheet.sampleRows ?? []).map((row) => {
    const obj: Record<number, string> = {}
    row.forEach((cell, i) => {
      obj[i] = cell ?? ''
    })
    return obj
  })
}

function setAll(v: boolean): void {
  for (const c of props.state.candidates) c.selected = v
}
</script>

<style scoped lang="scss">
.sheet-picker {
  &__toolbar {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
    margin-bottom: 12px;
  }

  &__file {
    border: 1px solid var(--border-2);
    border-radius: var(--radius-card);
    margin-bottom: 12px;
    overflow: hidden;
  }

  &__file-head {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    background: var(--bg-page);
    border-bottom: 1px solid var(--border-2);
  }

  &__file-name {
    font-weight: 600;
    font-size: 13px;
  }

  &__row {
    padding: 8px 12px;
    border-bottom: 1px dashed var(--border-2);

    &:last-child {
      border-bottom: none;
    }

    &--skipped {
      opacity: 0.72;
    }
  }

  &__row-main {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  &__name {
    font-weight: 600;
    font-size: 13px;
    color: var(--text-1);
  }

  &__spacer {
    flex: 1 1 auto;
  }

  &__sample {
    margin-top: 8px;
    padding-left: 24px;
  }

  &__warn {
    font-size: 12px;
    line-height: 20px;
  }
}
</style>
