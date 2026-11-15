<template>
  <div class="mapper">
    <div class="mapper__head">
      <div class="mapper__title">
        <span class="mapper__name">{{ config.fileName }} / {{ config.sheetName }}</span>
        <el-tag size="small" type="info" effect="plain">
          {{ config.rowCount < 0 ? '行数未知' : `${config.rowCount} 行` }}
        </el-tag>
        <el-tag v-if="missing.length" size="small" type="danger" effect="plain">
          缺必填：{{ missing.join('、') }}
        </el-tag>
      </div>
      <div class="mapper__actions">
        <el-button size="small" @click="autoDetect">自动识别</el-button>
        <el-button size="small" type="primary" plain @click="emit('apply-to-others', config)">应用到其他表</el-button>
      </div>
    </div>

    <div class="mapper__controls">
      <span class="text-tertiary">表头行</span>
      <el-select v-model="config.headerRow" size="small" style="width: 88px">
        <el-option v-for="h in config.headerRowCandidates" :key="h" :value="h" :label="String(h)" />
      </el-select>

      <span class="text-tertiary">方向</span>
      <el-select v-model="config.directionMode" size="small" style="width: 130px">
        <el-option value="form_default" label="按表默认方向" />
        <el-option value="column" label="按收支方向列" />
        <el-option value="by_category" label="按分类名判定" />
      </el-select>
      <el-select v-if="config.directionMode !== 'column'" v-model="config.directionDefault" size="small" style="width: 100px">
        <el-option value="expense" label="支出" />
        <el-option value="income" label="收入" />
      </el-select>

      <span class="text-tertiary">时间精度</span>
      <el-select v-model="config.timePrecision" size="small" style="width: 110px">
        <el-option value="day" label="仅到日" />
        <el-option value="second" label="精确到秒" />
      </el-select>
      <span v-if="!config.dateHasTime" class="text-tertiary">源数据无时间，将按当日顺序平均分布</span>
    </div>

    <el-table :data="rows" size="small" border class="mapper__table">
      <el-table-column label="目标字段" width="130">
        <template #default="{ row }">
          <span :class="{ 'mapper__req': row.required }">{{ row.label }}</span>
          <span v-if="row.required" class="mapper__star">*</span>
        </template>
      </el-table-column>
      <el-table-column label="来源列" min-width="180">
        <template #default="{ row }">
          <el-select
            :model-value="getMapping(row.key)"
            size="small"
            style="width: 100%"
            @update:model-value="setMapping(row.key, $event)"
          >
            <el-option value="" label="不导入" />
            <el-option
              v-for="col in config.columns"
              :key="col"
              :value="col"
              :label="col"
              :disabled="isColumnTaken(col, row.key)"
            />
          </el-select>
        </template>
      </el-table-column>
      <el-table-column label="状态" min-width="200">
        <template #default="{ row }">
          <span :class="statusClass(row.key)">{{ statusText(row.key) }}</span>
          <el-tag v-if="isSuggested(row.key)" size="small" type="success" effect="plain" style="margin-left: 6px">建议</el-tag>
        </template>
      </el-table-column>
    </el-table>

    <div v-if="validation.errors.length" class="mapper__errors">
      <div v-for="(e, i) in validation.errors" :key="i">{{ e }}</div>
    </div>
    <div v-else-if="validation.warnings.length" class="mapper__warnings">
      <div v-for="(w, i) in validation.warnings" :key="i">{{ w }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 单表列映射卡片（ARCHITECTURE-V12 §5.3）：
 * - 下拉选项 = ['不导入', ...columns]；已被其它字段占用的列置灰，防重复映射；
 * - 建议映射命中时打「建议」标；必填缺失时红字 + 卡片头红点（由父级折叠头渲染）；
 * - 「应用到其他表」由父级按列名批量套用。
 */
import { computed } from 'vue'
import { MAPPING_FIELDS, validateMapping, type MappingFieldKey } from '@/utils/sheetMapping'
import type { SheetMapping } from '@/api/types'
import type { SheetConfig } from './types'

const props = defineProps<{ config: SheetConfig }>()
const emit = defineEmits<{ 'apply-to-others': [config: SheetConfig] }>()

interface RowMeta {
  key: MappingFieldKey
  label: string
  required: boolean
}

const rows: RowMeta[] = MAPPING_FIELDS.map((f) => ({ key: f.key, label: f.label, required: f.required }))

const validation = computed(() =>
  validateMapping(props.config.mapping, { defaultDirection: props.config.directionDefault }),
)
const missing = computed(() => validation.value.missing)

/** el-table 作用域插槽的 row.key 是 any，统一收敛为 MappingFieldKey。 */
function asKey(key: unknown): MappingFieldKey {
  return key as MappingFieldKey
}

/** 当前字段选中的来源列（'' 表示不导入）。 */
function getMapping(key: unknown): string {
  return props.config.mapping[asKey(key)] ?? ''
}

/** 某列是否已被「其他」字段占用。 */
function isColumnTaken(col: string, except: MappingFieldKey): boolean {
  return MAPPING_FIELDS.some((f) => f.key !== except && props.config.mapping[f.key] === col)
}

function setMapping(rawKey: unknown, value: string): void {
  const key = asKey(rawKey)
  const mapping: SheetMapping = { ...props.config.mapping }
  mapping[key] = value ? value : null
  props.config.mapping = mapping
  // 手动映射了方向列 → 自动切到 column 模式；清空则回退到默认方向
  if (key === 'direction') {
    props.config.directionMode = value ? 'column' : 'form_default'
  }
}

function isSuggested(rawKey: unknown): boolean {
  const key = asKey(rawKey)
  const s = props.config.suggestedMapping[key]
  return Boolean(s) && s === props.config.mapping[key]
}

/** 状态列文案：优先反馈问题，其次说明回退行为。 */
function statusText(rawKey: unknown): string {
  const key = asKey(rawKey)
  const mapped = props.config.mapping[key]
  if (mapped) {
    const conflict = isColumnTaken(mapped, key)
    return conflict ? '该列已被其它字段占用' : `已映射「${mapped}」`
  }
  switch (key) {
    case 'date':
      return props.config.mapping.month ? '使用「年份月」，按当月 1 日' : '必填，请选择日期列'
    case 'amount':
      return '必填，请选择金额列'
    case 'direction':
      return `用默认：${props.config.directionDefault === 'income' ? '收入' : '支出'}`
    case 'account':
      return '用默认账户'
    case 'external_id':
      return '将生成内容指纹'
    case 'month':
      return '—'
    default:
      return '不导入'
  }
}

function statusClass(rawKey: unknown): string {
  const key = asKey(rawKey)
  const mapped = props.config.mapping[key]
  if (mapped && isColumnTaken(mapped, key)) return 'mapper__status--error'
  if (!mapped && (key === 'date' || key === 'amount')) {
    // 日期可由 month 兜底
    if (key === 'date' && props.config.mapping.month) return 'mapper__status--warn'
    return 'mapper__status--error'
  }
  if (!mapped) return 'mapper__status--muted'
  return 'mapper__status--ok'
}

/** 自动识别：恢复 probe 的建议映射（用户改乱后一键还原）。 */
function autoDetect(): void {
  props.config.mapping = { ...props.config.suggestedMapping }
  props.config.directionMode = props.config.mapping.direction ? 'column' : 'form_default'
}
</script>

<style scoped lang="scss">
.mapper {
  &__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 8px;
  }

  &__title {
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

  &__controls {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 8px;
  }

  &__req {
    color: var(--text-1);
  }

  &__star {
    color: var(--expense);
    margin-left: 2px;
  }

  &__status--ok {
    color: var(--income);
  }

  &__status--warn {
    color: var(--frozen);
  }

  &__status--error {
    color: var(--expense);
  }

  &__status--muted {
    color: var(--text-3);
  }

  &__errors {
    margin-top: 8px;
    font-size: 12px;
    color: var(--expense);
    line-height: 20px;
  }

  &__warnings {
    margin-top: 8px;
    font-size: 12px;
    color: var(--text-3);
    line-height: 20px;
  }
}
</style>
