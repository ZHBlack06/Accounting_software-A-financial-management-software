<template>
  <div class="rule-editor">
    <div class="rule-editor__head">
      <span class="text-secondary">匹配方式</span>
      <el-radio-group :model-value="mode" size="small" @update:model-value="onModeChange">
        <el-radio-button value="or">任一命中</el-radio-button>
        <el-radio-button value="all">全部命中</el-radio-button>
      </el-radio-group>
    </div>

    <div v-for="t in TARGET_TYPES" :key="t.value" class="rule-editor__row">
      <span class="rule-editor__label">{{ t.label }}</span>
      <el-select
        :model-value="local[t.value]"
        multiple
        collapse-tags
        collapse-tags-tooltip
        filterable
        clearable
        :placeholder="`选择${t.label}（可多选）`"
        style="flex: 1"
        @update:model-value="(v: string[]) => onSelectChange(t.value, v)"
      >
        <el-option v-for="o in optionsOf(t.value)" :key="o.id" :value="o.id" :label="o.name" />
      </el-select>
    </div>

    <div class="rule-editor__actions">
      <span class="text-tertiary">
        已配置 {{ targetCount }} 个目标；保存后视图重建，交易数立即刷新
      </span>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 账本规则编辑器（T10 验收 5）：5 类 target_type 多选 + or/all。
 * 变更通过 emit('update:rules') 回传，保存由父组件统一调用 PUT /ledgers/:id/rules。
 */
import { computed, reactive, watch } from 'vue'
import { useMasterStore } from '@/stores/master'
import type { LedgerRule, LedgerRuleTarget } from '@/api/types'

type TargetType = LedgerRuleTarget['target_type']

const TARGET_TYPES: { value: TargetType; label: string }[] = [
  { value: 'category_l1', label: '一级分类' },
  { value: 'category_l2', label: '二级分类' },
  { value: 'tag', label: '标签' },
  { value: 'account', label: '账户' },
  { value: 'merchant', label: '商户' },
]

const props = defineProps<{ rules: Omit<LedgerRule, 'id'>[] }>()
const emit = defineEmits<{ 'update:rules': [rules: Omit<LedgerRule, 'id'>[]] }>()

const master = useMasterStore()

const local = reactive<Record<TargetType, string[]>>({
  category_l1: [],
  category_l2: [],
  tag: [],
  account: [],
  merchant: [],
})
const mode = reactive<{ value: 'or' | 'all' }>({ value: 'or' })

/** 从 props 同步（父级加载/刷新时）。 */
function syncFromProps(): void {
  for (const t of TARGET_TYPES) local[t.value] = []
  for (const rule of props.rules) {
    for (const target of rule.targets) local[target.target_type].push(target.target_id)
  }
  mode.value = props.rules[0]?.match_mode ?? 'or'
}

syncFromProps()
watch(() => props.rules, syncFromProps, { deep: true })

const targetCount = computed(() => TARGET_TYPES.reduce((n, t) => n + local[t.value].length, 0))

function optionsOf(type: TargetType): { id: string; name: string }[] {
  switch (type) {
    case 'category_l1':
      return master.categories.map((c) => ({ id: c.id, name: c.name }))
    case 'category_l2':
      return master.categories.flatMap((c) =>
        (c.children ?? []).map((s) => ({ id: s.id, name: `${c.name} / ${s.name}` })),
      )
    case 'tag':
      return master.tags.map((t) => ({ id: t.id, name: t.name }))
    case 'account':
      return master.accounts.map((a) => ({ id: a.id, name: a.display_name }))
    case 'merchant':
      return master.merchants.map((m) => ({ id: m.id, name: m.name }))
    default:
      return []
  }
}

function buildTargets(): LedgerRuleTarget[] {
  const out: LedgerRuleTarget[] = []
  for (const t of TARGET_TYPES) {
    for (const id of local[t.value]) out.push({ target_type: t.value, target_id: id })
  }
  return out
}

function emitRules(): void {
  const targets = buildTargets()
  emit('update:rules', targets.length ? [{ match_mode: mode.value, targets }] : [])
}

function onSelectChange(type: TargetType, values: string[]): void {
  local[type] = values
  emitRules()
}

function onModeChange(v: string | number | boolean | undefined): void {
  mode.value = v as 'or' | 'all'
  emitRules()
}
</script>

<style scoped lang="scss">
.rule-editor {
  &__head {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 12px;
  }

  &__row {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 10px;
  }

  &__label {
    width: 80px;
    font-size: 13px;
    color: var(--text-2);
  }

  &__actions {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 8px;
  }
}
</style>
