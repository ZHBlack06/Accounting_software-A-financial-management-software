<template>
  <div class="template-bar">
    <el-tooltip v-for="t in templates" :key="t.id" :content="tooltip(t)" placement="top">
      <el-button
        size="small"
        round
        class="template-bar__btn"
        @click="emit('apply', t)"
        @contextmenu.prevent="openEdit(t)"
      >
        {{ t.name }}
      </el-button>
    </el-tooltip>
    <el-button size="small" round text type="primary" @click="openEdit(null)">＋模板</el-button>

    <el-dialog v-model="editOpen" :title="editing?.id && !isNew ? '编辑模板' : '新建模板'" width="360px" append-to-body>
      <el-form label-width="64px">
        <el-form-item label="名称" required>
          <el-input v-model="editForm.name" maxlength="10" placeholder="如：早餐" />
        </el-form-item>
        <el-form-item label="金额">
          <el-input v-model="editForm.amountYuan" placeholder="元，可空" @input="editForm.amountYuan = sanitizeYuanInput(editForm.amountYuan)" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button v-if="!isNew" type="danger" text @click="removeTemplate">删除</el-button>
        <el-button @click="editOpen = false">取消</el-button>
        <el-button type="primary" @click="saveTemplate">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
/**
 * 常用模板条（PRD 5.5）：点击一键填充，右键/长按编辑，模板来自 settings.quick_templates。
 */
import { computed, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { formatCents, parseYuan, sanitizeYuanInput } from '@/utils/money'
import { useDraftStore } from '@/stores/draft'
import type { QuickTemplate } from '@/api/types'

defineProps<{ templates: QuickTemplate[] }>()
const emit = defineEmits<{ apply: [tpl: QuickTemplate] }>()

const draft = useDraftStore()
const editOpen = ref(false)
const editing = ref<QuickTemplate | null>(null)
const editForm = reactive({ name: '', amountYuan: '' })

const isNew = computed(() => editing.value !== null && !draft.templates.some((t) => t.id === editing.value?.id))

function tooltip(t: QuickTemplate): string {
  return t.amount_cents ? `${t.name} ${formatCents(t.amount_cents)}（右键编辑）` : `${t.name}（右键编辑）`
}

function openEdit(t: QuickTemplate | null): void {
  editing.value = t
  editForm.name = t?.name ?? ''
  editForm.amountYuan = t?.amount_cents != null ? (t.amount_cents / 100).toFixed(2) : ''
  editOpen.value = true
}

async function saveTemplate(): Promise<void> {
  if (!editForm.name.trim()) {
    ElMessage.warning('模板名称必填')
    return
  }
  const list = [...draft.templates]
  const amountCents = editForm.amountYuan ? parseYuan(editForm.amountYuan) : null
  if (editing.value && !isNew.value) {
    const idx = list.findIndex((t) => t.id === editing.value?.id)
    if (idx >= 0) list[idx] = { ...list[idx], name: editForm.name.trim(), amount_cents: amountCents }
  } else {
    list.push({
      id: `tpl_${Date.now().toString(36)}`,
      name: editForm.name.trim(),
      amount_cents: amountCents,
      account_id: null,
      category_id: null,
      merchant_id: null,
      sort_order: list.length,
    })
  }
  await persist(list)
  editOpen.value = false
}

async function removeTemplate(): Promise<void> {
  const list = draft.templates.filter((t) => t.id !== editing.value?.id)
  await persist(list)
  editOpen.value = false
}

async function persist(list: QuickTemplate[]): Promise<void> {
  try {
    await draft.saveTemplates(list.map((t, i) => ({ ...t, sort_order: i })))
    ElMessage.success('模板已保存')
  } catch {
    ElMessage.warning('模板暂存本地（服务端保存失败）')
  }
}
</script>

<style scoped lang="scss">
.template-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 8px;
}
</style>
