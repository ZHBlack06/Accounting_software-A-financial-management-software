<template>
  <div class="mapping-step">
    <el-alert
      v-if="!state.configs.length"
      type="warning"
      show-icon
      :closable="false"
      title="尚未勾选任何工作表，请返回上一步勾选"
    />

    <el-collapse v-else v-model="activeNames">
      <el-collapse-item v-for="config in state.configs" :key="config.key" :name="config.key">
        <template #title>
          <div class="mapping-step__head">
            <span v-if="!isValid(config)" class="mapping-step__dot" />
            <span class="mapping-step__label">{{ config.fileName }} / {{ config.sheetName }}</span>
            <el-tag size="small" type="info" effect="plain">
              {{ config.rowCount < 0 ? '行数未知' : `${config.rowCount} 行` }}
            </el-tag>
            <el-tag v-if="!isValid(config)" size="small" type="danger" effect="plain">
              {{ missingText(config) }}
            </el-tag>
            <el-tag v-else size="small" type="success" effect="plain">可导入</el-tag>
          </div>
        </template>
        <ColumnMapper :config="config" @apply-to-others="onApplyToOthers" />
      </el-collapse-item>
    </el-collapse>

    <div v-if="state.configs.length" class="mapping-step__footer">
      <span :class="allValid ? 'text-tertiary' : 'mapping-step__blocker'">
        {{ allValid ? '映射校验通过，可进入预览' : `还有 ${invalidCount} 张表缺少必填映射（日期 / 金额）` }}
      </span>
      <span class="text-tertiary">每张表的「应用到其他表」按列名相同批量套用，可省大量点击</span>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 步 3：列映射（ARCHITECTURE-V12 §5.3）。
 * 每张已选表一张折叠卡片；必填缺失时卡片头红点 + 「下一步」禁用（禁用逻辑在向导壳里）。
 */
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { applyMappingByColumnName, validateMapping } from '@/utils/sheetMapping'
import ColumnMapper from './ColumnMapper.vue'
import type { SheetConfig, WizardState } from './types'

const props = defineProps<{ state: WizardState }>()

const activeNames = ref<string[]>(props.state.configs.map((c) => c.key))

// 新增表单时自动展开（回退再前进时保留用户折叠状态）
watch(
  () => props.state.configs.map((c) => c.key).join(','),
  () => {
    const missing = props.state.configs.filter((c) => !activeNames.value.includes(c.key))
    if (missing.length) activeNames.value = [...activeNames.value, ...missing.map((c) => c.key)]
  },
)

function isValid(config: SheetConfig): boolean {
  return validateMapping(config.mapping, { defaultDirection: config.directionDefault }).errors.length === 0
}

function missingText(config: SheetConfig): string {
  const { missing } = validateMapping(config.mapping)
  return `缺必填：${missing.join('、')}`
}

const invalidCount = computed(() => props.state.configs.filter((c) => !isValid(c)).length)
const allValid = computed(() => invalidCount.value === 0)

/** 「应用到其他表」：按列名相同批量套用（不含源表自身）。 */
function onApplyToOthers(source: SheetConfig): void {
  let touched = 0
  for (const config of props.state.configs) {
    if (config.key === source.key) continue
    const { mapping, applied } = applyMappingByColumnName(source.mapping, config.columns)
    if (!applied.length) continue
    config.mapping = mapping
    config.directionMode = mapping.direction ? 'column' : config.directionMode
    touched += 1
  }
  if (touched) ElMessage.success(`已按列名套用到 ${touched} 张其它表（仅套用列名相同的字段）`)
  else ElMessage.warning('其它表的列名与当前表没有交集，无法套用')
}
</script>

<style scoped lang="scss">
.mapping-step {
  &__head {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  &__label {
    font-size: 13px;
    font-weight: 600;
    color: var(--text-1);
  }

  &__dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--expense);
    display: inline-block;
  }

  &__footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
    margin-top: 12px;
  }

  &__blocker {
    font-size: 12px;
    color: var(--expense);
  }
}
</style>
