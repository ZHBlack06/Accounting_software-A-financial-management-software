<template>
  <div>
    <div class="panel-toolbar">
      <el-radio-group v-model="txnType" size="small">
        <el-radio-button value="expense">支出分类</el-radio-button>
        <el-radio-button value="income">收入分类</el-radio-button>
      </el-radio-group>
      <el-button type="primary" size="small" @click="openCreate(null)">＋新建一级分类</el-button>
      <span class="text-tertiary">二级分类只能挂在同类型的（支出/收入）一级分类下；颜色与图表调色板同源</span>
    </div>

    <el-table
      v-loading="loading"
      :data="roots"
      size="small"
      row-key="id"
      default-expand-all
      :tree-props="{ children: 'children' }"
    >
      <el-table-column label="分类" min-width="200">
        <template #default="{ row }">
          <span :style="{ color: row.color || undefined }">●</span> {{ row.name }}
          <el-tag v-if="row.level === 2" size="small" type="info" style="margin-left: 6px">二级</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="颜色" width="120">
        <template #default="{ row }">
          <span class="color-dot" :style="{ background: row.color }" />
          <span class="text-tertiary">{{ row.color }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="sort_order" label="排序" width="80" align="right" />
      <el-table-column label="操作" width="200" fixed="right">
        <template #default="{ row }">
          <el-button v-if="row.level === 1" link size="small" type="primary" @click="openCreate(row)">加二级</el-button>
          <el-button link size="small" type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link size="small" type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogOpen" :title="dialogTitle" width="420px" append-to-body>
      <el-form ref="formRef" :model="form" label-width="80px" :rules="rules">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" maxlength="20" />
        </el-form-item>
        <el-form-item label="上级">
          <el-select v-model="form.parent_id" clearable placeholder="无（一级分类）" style="width: 100%" @change="onParentChange">
            <el-option v-for="c in roots" :key="c.id" :value="c.id" :label="c.name" />
          </el-select>
        </el-form-item>
        <el-form-item label="类型">
          <el-radio-group v-model="form.transaction_type" :disabled="!!form.parent_id">
            <el-radio-button value="expense">支出</el-radio-button>
            <el-radio-button value="income">收入</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="颜色">
          <div class="color-picker">
            <span
              v-for="c in PALETTE"
              :key="c"
              class="color-dot color-dot--pick"
              :class="{ 'color-dot--active': form.color === c }"
              :style="{ background: c }"
              @click="form.color = c"
            />
          </div>
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="form.sort_order" :min="0" :step="10" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
/**
 * 分类设置（T10 验收 4）：两级树、二级只能挂同 transaction_type 的一级下、
 * 10 色预选与图表 PALETTE 同源、删除被引用时引导归档。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { createCategory, deleteCategory, listCategories, updateCategory } from '@/api/categories'
import { PALETTE } from '@/utils/echarts'
import { errorText, ApiError } from '@/api/client'
import type { CategoryDTO, CategoryNode, TransactionType } from '@/api/types'

const loading = ref(false)
const saving = ref(false)
const txnType = ref<TransactionType>('expense')
const nodes = ref<CategoryNode[]>([])
const dialogOpen = ref(false)
const editing = ref<CategoryDTO | null>(null)
const formRef = ref<FormInstance>()

const form = reactive({
  name: '',
  parent_id: null as string | null,
  transaction_type: 'expense' as TransactionType,
  color: PALETTE[0],
  sort_order: 0,
})

const rules: FormRules = { name: [{ required: true, message: '名称必填', trigger: 'blur' }] }

const roots = computed(() => nodes.value.filter((c) => c.transaction_type === txnType.value))
const dialogTitle = computed(() => (editing.value ? '编辑分类' : form.parent_id ? '新建二级分类' : '新建一级分类'))

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await listCategories(undefined, true)
    nodes.value = res.items
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    loading.value = false
  }
}

function openCreate(parent: CategoryNode | null): void {
  editing.value = null
  Object.assign(form, {
    name: '',
    parent_id: parent?.id ?? null,
    transaction_type: parent?.transaction_type ?? txnType.value,
    color: parent?.color ?? PALETTE[0],
    sort_order: 0,
  })
  dialogOpen.value = true
}

function openEdit(row: CategoryDTO): void {
  editing.value = row
  Object.assign(form, {
    name: row.name,
    parent_id: row.parent_id,
    transaction_type: row.transaction_type,
    color: row.color || PALETTE[0],
    sort_order: row.sort_order,
  })
  dialogOpen.value = true
}

/** 选父级后，类型/颜色跟随父级（二级必须与一级同 transaction_type）。 */
function onParentChange(parentId: string | null): void {
  if (!parentId) return
  const parent = roots.value.find((c) => c.id === parentId)
  if (parent) {
    form.transaction_type = parent.transaction_type
    form.color = parent.color || form.color
  }
}

async function save(): Promise<void> {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  saving.value = true
  try {
    if (editing.value) {
      await updateCategory(editing.value.id, {
        name: form.name.trim(),
        color: form.color,
        sort_order: form.sort_order,
      })
      ElMessage.success('分类已更新')
    } else {
      await createCategory({
        name: form.name.trim(),
        parent_id: form.parent_id,
        transaction_type: form.transaction_type,
        color: form.color,
        sort_order: form.sort_order,
      })
      ElMessage.success('分类已创建')
    }
    dialogOpen.value = false
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    saving.value = false
  }
}

async function remove(row: CategoryDTO): Promise<void> {
  try {
    await ElMessageBox.confirm(`确定删除分类「${row.name}」？被引用时只能归档。`, '删除分类', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await deleteCategory(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'CATEGORY_IN_USE') {
      try {
        await ElMessageBox.confirm('该分类已被交易引用，不能删除。是否归档？', '无法删除', {
          confirmButtonText: '归档',
          cancelButtonText: '取消',
          type: 'warning',
        })
        await updateCategory(row.id, { is_archived: 1 })
        ElMessage.success('已归档')
        await load()
      } catch {
        /* 取消 */
      }
    } else {
      ElMessage.error(errorText(e))
    }
  }
}

onMounted(load)
</script>

<style scoped lang="scss">
.panel-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.color-dot {
  display: inline-block;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  margin-right: 4px;
  vertical-align: -2px;
}

.color-picker {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.color-dot--pick {
  width: 22px;
  height: 22px;
  cursor: pointer;
  border: 2px solid transparent;

  &--active {
    border-color: var(--text-1);
  }
}
</style>
