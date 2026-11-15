<template>
  <div>
    <div class="panel-toolbar">
      <el-input v-model="q" placeholder="搜索标签" clearable style="width: 180px" @input="onSearch" />
      <el-button type="primary" size="small" @click="openCreate">＋新建标签</el-button>
      <span class="text-tertiary">标签被交易引用时不可删除，可改为归档</span>
    </div>
    <el-table v-loading="loading" :data="master.tags" size="small">
      <el-table-column label="标签" min-width="140">
        <template #default="{ row }">
          <el-tag size="small" :color="row.color + '22'" style="border-color: transparent">
            <span :style="{ color: row.color }">{{ row.name }}</span>
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="颜色" width="120">
        <template #default="{ row }">
          <span class="color-dot" :style="{ background: row.color }" />
          <span class="text-tertiary">{{ row.color }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="use_count" label="使用次数" width="100" align="right" />
      <el-table-column label="操作" width="160" fixed="right">
        <template #default="{ row }">
          <el-button link size="small" type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link size="small" type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogOpen" :title="editing ? '编辑标签' : '新建标签'" width="380px" append-to-body>
      <el-form label-width="70px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" maxlength="20" />
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
      </el-form>
      <template #footer>
        <el-button @click="dialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
/** 标签设置（T10）：CRUD + 颜色 + 使用次数。 */
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { createTag, deleteTag, listTags, updateTag } from '@/api/tags'
import { useMasterStore } from '@/stores/master'
import { PALETTE } from '@/utils/echarts'
import { errorText } from '@/api/client'
import type { TagDTO } from '@/api/types'

const master = useMasterStore()
const loading = ref(false)
const saving = ref(false)
const q = ref('')
const dialogOpen = ref(false)
const editing = ref<TagDTO | null>(null)
const form = reactive({ name: '', color: PALETTE[0] })

let searchTimer: ReturnType<typeof setTimeout> | null = null

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await listTags(q.value)
    master.tags = res.items
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    loading.value = false
  }
}

function onSearch(): void {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(load, 300)
}

function openCreate(): void {
  editing.value = null
  form.name = ''
  form.color = PALETTE[master.tags.length % PALETTE.length]
  dialogOpen.value = true
}

function openEdit(row: TagDTO): void {
  editing.value = row
  form.name = row.name
  form.color = row.color
  dialogOpen.value = true
}

async function save(): Promise<void> {
  if (!form.name.trim()) {
    ElMessage.warning('标签名称必填')
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      await updateTag(editing.value.id, { name: form.name.trim(), color: form.color })
      ElMessage.success('标签已更新')
    } else {
      await createTag({ name: form.name.trim(), color: form.color })
      ElMessage.success('标签已创建')
    }
    dialogOpen.value = false
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    saving.value = false
  }
}

async function remove(row: TagDTO): Promise<void> {
  try {
    await ElMessageBox.confirm(`确定删除标签「${row.name}」？`, '删除标签', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await deleteTag(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
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
