<template>
  <div class="item-editor">
    <el-table :data="rows" size="small" style="width: 100%" empty-text="暂无物品明细（可空）">
      <el-table-column label="物品名称" min-width="140">
        <template #default="{ row }">
          <el-input v-model="row.name" size="small" placeholder="名称" maxlength="50" />
        </template>
      </el-table-column>
      <el-table-column label="原价(元)" width="110">
        <template #default="{ row }">
          <el-input v-model="row.originalYuan" size="small" @input="row.originalYuan = sanitizeYuanInput(row.originalYuan)" />
        </template>
      </el-table-column>
      <el-table-column label="实付(元)" width="110">
        <template #default="{ row }">
          <el-input v-model="row.paidYuan" size="small" @input="row.paidYuan = sanitizeYuanInput(row.paidYuan)" />
        </template>
      </el-table-column>
      <el-table-column label="折扣" width="80" align="right">
        <template #default="{ row }">
          <span class="text-secondary">{{ discountText(row) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="折扣理由" min-width="120">
        <template #default="{ row }">
          <el-input v-model="row.discount_reason" size="small" placeholder="如：红包减免" maxlength="50" />
        </template>
      </el-table-column>
      <el-table-column label="" width="96" align="center">
        <template #default="{ $index }">
          <el-button link size="small" :disabled="$index === 0" @click="move($index, -1)">↑</el-button>
          <el-button link size="small" :disabled="$index === rows.length - 1" @click="move($index, 1)">↓</el-button>
          <el-button link size="small" type="danger" @click="rows.splice($index, 1)">删</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-button size="small" style="margin-top: 8px" @click="addRow">＋添加物品</el-button>
  </div>
</template>

<script setup lang="ts">
/**
 * 多物品明细编辑器：原价/实付输入元，折扣自动算，上下箭头排序，允许空明细。
 *
 * v1.2 T08（R4 表格溢出修复）：
 * - 列最小宽度合计 140+110+110+80+120+96 = **656px**（修复前 120+120+120+90+100+90 = 640px，但容器仅约 460–512px）；
 * - 容器 `.item-editor` 显式 `width:100% / max-width:100% / min-width:0 / overflow-x:auto`，
 *   这样父级 flex 子项不会被子元素撑开，宽度不足时**在表格内部**横向滚动而不是顶出弹窗；
 * - 表格 `style="width:100%"`，el-table 自身在列宽合计 > 容器宽度时产生内部横向滚动条。
 */
import { ref, watch } from 'vue'
import { formatCents, parseYuan, sanitizeYuanInput } from '@/utils/money'
import type { TransactionItemDTO } from '@/api/types'

interface Row {
  name: string
  originalYuan: string
  paidYuan: string
  discount_reason: string
}

const props = defineProps<{ modelValue: Omit<TransactionItemDTO, 'id'>[] }>()
const emit = defineEmits<{ 'update:modelValue': [v: Omit<TransactionItemDTO, 'id'>[]] }>()

const rows = ref<Row[]>(fromModel(props.modelValue))
let syncing = false

watch(
  () => props.modelValue,
  (v) => {
    if (syncing) return
    rows.value = fromModel(v)
  },
  { deep: true },
)

watch(
  rows,
  () => {
    syncing = true
    emit(
      'update:modelValue',
      rows.value
        .filter((r) => r.name.trim() || r.paidYuan)
        .map((r, i) => ({
          name: r.name.trim(),
          sub_category_id: null,
          original_amount_cents: parseYuan(r.originalYuan) ?? parseYuan(r.paidYuan) ?? 0,
          paid_amount_cents: parseYuan(r.paidYuan) ?? 0,
          discount_reason: r.discount_reason.trim(),
          sort_order: i,
        })),
    )
    queueMicrotask(() => {
      syncing = false
    })
  },
  { deep: true },
)

function fromModel(items: Omit<TransactionItemDTO, 'id'>[]): Row[] {
  return items.map((it) => ({
    name: it.name,
    originalYuan: it.original_amount_cents ? (it.original_amount_cents / 100).toFixed(2) : '',
    paidYuan: it.paid_amount_cents ? (it.paid_amount_cents / 100).toFixed(2) : '',
    discount_reason: it.discount_reason ?? '',
  }))
}

function discountText(r: Row): string {
  const orig = parseYuan(r.originalYuan)
  const paid = parseYuan(r.paidYuan)
  if (orig === null || paid === null) return '—'
  const d = orig - paid
  return d > 0 ? `-${formatCents(d)}` : '无'
}

function addRow(): void {
  rows.value.push({ name: '', originalYuan: '', paidYuan: '', discount_reason: '' })
}

function move(idx: number, dir: -1 | 1): void {
  const target = idx + dir
  if (target < 0 || target >= rows.value.length) return
  const tmp = rows.value[idx]
  rows.value[idx] = rows.value[target]
  rows.value[target] = tmp
}
</script>

<style scoped lang="scss">
/* 关键修复：容器必须是可收缩的 flex 子项，并且横向溢出自己滚动，而不是撑大父容器 */
.item-editor {
  width: 100%;
  max-width: 100%;
  min-width: 0;
  overflow-x: auto;
}
</style>
