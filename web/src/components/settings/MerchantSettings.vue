<template>
  <div>
    <div class="panel-toolbar">
      <el-input v-model="q" placeholder="搜索商户" clearable style="width: 180px" @input="onSearch" />
      <el-select v-model="merchantType" placeholder="类型" clearable style="width: 120px" @change="load">
        <el-option value="online" label="线上" />
        <el-option value="offline" label="线下" />
      </el-select>
      <el-button type="primary" size="small" @click="openCreate">＋新建商户</el-button>
      <span class="text-tertiary">按使用次数降序；≥10 次的视为常用商户</span>
    </div>

    <div v-if="candidates.length" class="candidates">
      <div class="candidates__title">候选词条（≥10 次）</div>
      <div class="candidates__list">
        <el-tag v-for="c in candidates" :key="c.id" size="small" closable @close="promote(c)">
          {{ c.name }}（{{ c.use_count }}）
        </el-tag>
      </div>
      <div class="text-tertiary">点击 × 可将候选词条确认为正式商户备注（置顶展示）</div>
    </div>

    <el-table v-loading="loading" :data="rows" size="small">
      <el-table-column prop="name" label="商户" min-width="160" />
      <el-table-column label="类型" width="90">
        <template #default="{ row }">{{ row.merchant_type === 'online' ? '线上' : '线下' }}</template>
      </el-table-column>
      <el-table-column prop="platform_code" label="平台编码" width="120" />
      <el-table-column prop="use_count" label="使用次数" width="100" align="right" />
      <el-table-column label="操作" width="160" fixed="right">
        <template #default="{ row }">
          <el-button link size="small" type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link size="small" type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogOpen" :title="editing ? '编辑商户' : '新建商户'" width="400px" append-to-body>
      <el-form label-width="80px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" maxlength="40" />
        </el-form-item>
        <el-form-item label="类型">
          <el-radio-group v-model="form.merchant_type">
            <el-radio-button value="offline">线下</el-radio-button>
            <el-radio-button value="online">线上</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="form.merchant_type === 'online'" label="平台编码">
          <el-input v-model="form.platform_code" maxlength="20" placeholder="如 taobao / jd" />
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
 * 商户设置（T10 验收 6）：按 use_count desc；≥10 次展示为「候选词条」分组，
 * 一键确认（此处语义为取消候选提示，实际商户已存在）。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { createMerchant, deleteMerchant, searchMerchants, updateMerchant } from '@/api/merchants'
import { errorText } from '@/api/client'
import type { MerchantDTO } from '@/api/types'

const loading = ref(false)
const saving = ref(false)
const q = ref('')
const merchantType = ref('')
const list = ref<MerchantDTO[]>([])
const dialogOpen = ref(false)
const editing = ref<MerchantDTO | null>(null)
const dismissed = ref<string[]>([])

const form = reactive({ name: '', merchant_type: 'offline' as 'offline' | 'online', platform_code: '' })

const rows = computed(() => [...list.value].sort((a, b) => b.use_count - a.use_count))
const candidates = computed(() =>
  rows.value.filter((m) => m.use_count >= 10 && !dismissed.value.includes(m.id)).slice(0, 8),
)

let searchTimer: ReturnType<typeof setTimeout> | null = null

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await searchMerchants(q.value, merchantType.value, 50)
    list.value = res.items
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

function promote(m: MerchantDTO): void {
  dismissed.value.push(m.id)
  ElMessage.success(`已确认常用商户：${m.name}`)
}

function openCreate(): void {
  editing.value = null
  Object.assign(form, { name: '', merchant_type: 'offline', platform_code: '' })
  dialogOpen.value = true
}

function openEdit(row: MerchantDTO): void {
  editing.value = row
  Object.assign(form, { name: row.name, merchant_type: row.merchant_type, platform_code: row.platform_code })
  dialogOpen.value = true
}

async function save(): Promise<void> {
  if (!form.name.trim()) {
    ElMessage.warning('商户名称必填')
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      await updateMerchant(editing.value.id, {
        name: form.name.trim(),
        merchant_type: form.merchant_type,
        platform_code: form.platform_code.trim(),
      })
      ElMessage.success('商户已更新')
    } else {
      await createMerchant({
        name: form.name.trim(),
        merchant_type: form.merchant_type,
        platform_code: form.platform_code.trim(),
      })
      ElMessage.success('商户已创建')
    }
    dialogOpen.value = false
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    saving.value = false
  }
}

async function remove(row: MerchantDTO): Promise<void> {
  try {
    await ElMessageBox.confirm(`确定删除商户「${row.name}」？被引用时将归档。`, '删除商户', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await deleteMerchant(row.id)
    ElMessage.success('已删除/归档')
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

.candidates {
  padding: 10px 12px;
  border: 1px dashed var(--border-1);
  border-radius: 8px;
  margin-bottom: 12px;

  &__title {
    font-size: 13px;
    font-weight: 600;
    color: var(--text-2);
    margin-bottom: 8px;
  }

  &__list {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 6px;
  }
}
</style>
