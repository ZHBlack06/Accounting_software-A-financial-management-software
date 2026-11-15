<template>
  <div>
    <div class="panel-toolbar">
      <el-button type="primary" size="small" @click="openCreate">＋新建账本</el-button>
      <span class="text-tertiary">账本通过规则（分类/标签/账户/商户）自动纳入交易；保存规则后交易数立即刷新</span>
    </div>
    <el-table v-loading="loading" :data="master.ledgers" size="small">
      <el-table-column label="账本" min-width="140">
        <template #default="{ row }">
          {{ row.name }}
          <el-tag v-if="row.is_default" size="small" type="success" style="margin-left: 6px">默认</el-tag>
          <el-tag v-if="row.is_archived" size="small" type="info" style="margin-left: 6px">已归档</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="transaction_count" label="交易数" width="90" align="right" />
      <el-table-column label="规则" min-width="200">
        <template #default="{ row }">
          <span v-if="!row.rules?.length" class="text-tertiary">未配置</span>
          <el-tag v-for="(r, i) in row.rules" :key="i" size="small" type="info" style="margin-right: 4px">
            {{ r.match_mode === 'or' ? '任一' : '全部' }} · {{ r.targets.length }} 项
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="220" fixed="right">
        <template #default="{ row }">
          <el-button link size="small" type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link size="small" type="warning" @click="openRules(row)">规则</el-button>
          <el-button link size="small" @click="toggleArchive(row)">{{ row.is_archived ? '取消归档' : '归档' }}</el-button>
          <el-button link size="small" type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogOpen" :title="editing ? '编辑账本' : '新建账本'" width="420px" append-to-body>
      <el-form label-width="80px">
        <el-form-item label="名称" required>
          <el-input v-model="form.name" maxlength="20" />
        </el-form-item>
        <el-form-item label="币种">
          <el-input v-model="form.currency" maxlength="8" placeholder="CNY" />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="form.remark" maxlength="100" />
        </el-form-item>
        <el-form-item label="设为默认">
          <el-switch v-model="form.is_default" :active-value="1" :inactive-value="0" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="rulesOpen" :title="`规则配置：${rulesTarget?.name ?? ''}`" width="560px" append-to-body>
      <LedgerRuleEditor v-if="rulesTarget" :rules="draftRules" @update:rules="draftRules = $event" />
      <template #footer>
        <el-button @click="rulesOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveRules">保存规则</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
/** 账本设置（T10 验收 5）：CRUD + 归档 + 默认账本 + 规则编辑器（保存后 transaction_count 刷新）。 */
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  archiveLedger,
  createLedger,
  deleteLedger,
  listLedgers,
  putLedgerRules,
  updateLedger,
} from '@/api/ledgers'
import { useMasterStore } from '@/stores/master'
import { errorText } from '@/api/client'
import type { LedgerDTO, LedgerRule } from '@/api/types'
import LedgerRuleEditor from './LedgerRuleEditor.vue'

const master = useMasterStore()
const loading = ref(false)
const saving = ref(false)
const dialogOpen = ref(false)
const rulesOpen = ref(false)
const editing = ref<LedgerDTO | null>(null)
const rulesTarget = ref<LedgerDTO | null>(null)
const draftRules = ref<Omit<LedgerRule, 'id'>[]>([])

const form = reactive({ name: '', currency: 'CNY', remark: '', is_default: 0 as 0 | 1 })

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await listLedgers()
    master.ledgers = res.items
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editing.value = null
  Object.assign(form, { name: '', currency: 'CNY', remark: '', is_default: 0 })
  dialogOpen.value = true
}

function openEdit(row: LedgerDTO): void {
  editing.value = row
  Object.assign(form, { name: row.name, currency: row.currency, remark: row.remark, is_default: row.is_default })
  dialogOpen.value = true
}

async function save(): Promise<void> {
  if (!form.name.trim()) {
    ElMessage.warning('账本名称必填')
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      await updateLedger(editing.value.id, { ...form, name: form.name.trim() })
      ElMessage.success('账本已更新')
    } else {
      await createLedger({ ...form, name: form.name.trim(), rules: [] })
      ElMessage.success('账本已创建')
    }
    dialogOpen.value = false
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    saving.value = false
  }
}

function openRules(row: LedgerDTO): void {
  rulesTarget.value = row
  draftRules.value = row.rules.map((r) => ({ match_mode: r.match_mode, targets: r.targets }))
  rulesOpen.value = true
}

async function saveRules(): Promise<void> {
  if (!rulesTarget.value) return
  saving.value = true
  try {
    await putLedgerRules(rulesTarget.value.id, draftRules.value)
    ElMessage.success('规则已保存，账本视图已重建')
    rulesOpen.value = false
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    saving.value = false
  }
}

async function toggleArchive(row: LedgerDTO): Promise<void> {
  try {
    await archiveLedger(row.id, !row.is_archived)
    ElMessage.success(row.is_archived ? '已取消归档' : '已归档')
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}

async function remove(row: LedgerDTO): Promise<void> {
  try {
    await ElMessageBox.confirm(`确定删除账本「${row.name}」？将同时删除视图。`, '删除账本', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await deleteLedger(row.id)
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
</style>
