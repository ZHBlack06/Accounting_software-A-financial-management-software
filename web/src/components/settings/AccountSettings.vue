<template>
  <div>
    <el-alert type="success" :closable="false" show-icon class="merge-hint">
      <template #title>
        只用一个账户？点「合并为总账户」把所有账户的历史并入「总账户」。
        <b>此操作不可撤销</b>，建议先到「备份」页创建一次备份。
      </template>
      <el-button size="small" type="primary" plain :disabled="master.accounts.length < 2" @click="openMerge">
        合并为总账户
      </el-button>
    </el-alert>

    <div class="panel-toolbar">
      <el-button type="primary" size="small" @click="openCreate">＋新建账户</el-button>
      <span class="text-tertiary">初始余额仅在该账户冻结区无交易时可修改；有交易的账户请用「归档」代替删除</span>
    </div>
    <el-table v-loading="loading" :data="master.accounts" size="small" row-key="id">
      <el-table-column label="排序" width="90" align="center">
        <template #default="{ $index }">
          <el-button link size="small" :disabled="$index === 0" @click="move($index, -1)">↑</el-button>
          <el-button link size="small" :disabled="$index === master.accounts.length - 1" @click="move($index, 1)">↓</el-button>
        </template>
      </el-table-column>
      <el-table-column prop="display_name" label="账户（自动拼接）" min-width="180" />
      <el-table-column prop="value_type" label="储值类型" width="100">
        <template #default="{ row }">{{ VALUE_TYPE_LABELS[row.value_type] ?? row.value_type }}</template>
      </el-table-column>
      <el-table-column label="初始余额" width="120" align="right">
        <template #default="{ row }"><MoneyText :cents="row.initial_balance_cents" /></template>
      </el-table-column>
      <el-table-column label="当前余额" width="130" align="right">
        <template #default="{ row }"><MoneyText :cents="row.balance_cents" :bold="true" /></template>
      </el-table-column>
      <el-table-column prop="transaction_count" label="交易数" width="80" align="right" />
      <el-table-column label="状态" width="80">
        <template #default="{ row }">
          <el-tag v-if="row.is_archived" size="small" type="info">已归档</el-tag>
          <el-tag v-else size="small" type="success">启用</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="160" fixed="right">
        <template #default="{ row }">
          <el-button link size="small" type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link size="small" @click="toggleArchive(row)">{{ row.is_archived ? '取消归档' : '归档' }}</el-button>
          <el-button link size="small" type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogOpen" :title="editing ? '编辑账户' : '新建账户'" width="440px" append-to-body>
      <el-form ref="formRef" :model="form" label-width="96px" :rules="rules">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" maxlength="30" placeholder="如：招商银行借记卡1" />
        </el-form-item>
        <el-form-item label="别名">
          <el-input v-model="form.alias" maxlength="20" placeholder="如：招行（可空）" />
        </el-form-item>
        <el-form-item label="银行名称">
          <el-input v-model="form.bank_name" maxlength="30" />
        </el-form-item>
        <el-form-item label="卡号后四位" prop="card_tail">
          <el-input v-model="form.card_tail" maxlength="4" placeholder="4 位数字（可空）" />
        </el-form-item>
        <el-form-item label="储值类型">
          <el-select v-model="form.value_type" style="width: 100%">
            <el-option v-for="(label, code) in VALUE_TYPE_LABELS" :key="code" :value="code" :label="label" />
          </el-select>
        </el-form-item>
        <el-form-item label="初始余额">
          <el-input v-model="form.initialYuan" :disabled="frozenLocked" @input="form.initialYuan = sanitizeYuanInput(form.initialYuan)">
            <template #prefix>¥</template>
          </el-input>
          <div v-if="frozenLocked" class="text-tertiary" style="margin-top: 4px">
            该账户存在冻结区交易，初始余额不可修改（409 FROZEN）
          </div>
        </el-form-item>
        <el-form-item label="计入总余额">
          <el-switch v-model="form.is_count_in_total" :active-value="1" :inactive-value="0" />
        </el-form-item>
        <el-form-item label="预览">
          <span class="text-secondary">{{ previewName }}</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="mergeOpen" title="合并为总账户" width="560px" append-to-body>
      <!-- 合并结果：直接展示后端 account_details（合并前后余额 + 笔数） -->
      <template v-if="mergeResult">
        <el-alert
          type="success"
          show-icon
          :closable="false"
          :title="`已合并 ${mergeResult.merged_accounts} 个账户、搬移 ${mergeResult.moved_transactions} 笔交易（转账对手行 ${mergeResult.moved_transfer_legs} 笔）`"
          style="margin-bottom: 12px"
        />
        <div class="text-secondary" style="margin-bottom: 8px">
          并入目标：<b>{{ mergeTargetLabel }}</b>
        </div>
        <el-table :data="mergeSourceDetails" size="small" border empty-text="没有需要展示的源账户">
          <el-table-column prop="name" label="源账户" min-width="150" />
          <el-table-column label="合并前余额" width="120" align="right">
            <template #default="{ row }">{{ formatCents(row.balance_before_cents) }}</template>
          </el-table-column>
          <el-table-column label="初始余额" width="110" align="right">
            <template #default="{ row }">{{ formatCents(row.initial_balance_cents) }}</template>
          </el-table-column>
          <el-table-column prop="transaction_count" label="笔数" width="70" align="right" />
        </el-table>
        <div class="text-tertiary" style="margin-top: 8px">
          合并后总初始余额 {{ formatCents(mergeResult.initial_balance_cents) }}；
          <template v-if="mergeResult.rematerialized">已重算 {{ mergeResult.rematerialize_rows }} 行余额</template>
          <template v-else>余额重算未触发</template>
        </div>
        <el-alert
          v-for="(w, i) in mergeResult.warnings ?? []"
          :key="i"
          type="warning"
          show-icon
          :closable="false"
          :title="w"
          style="margin-top: 8px"
        />
      </template>

      <template v-else>
      <el-alert
        type="warning"
        show-icon
        :closable="false"
        title="合并会先把所有源账户的交易与转账双边改写到目标账户，然后删除源账户并全量重算余额。此操作不可撤销。"
        style="margin-bottom: 12px"
      />
      <el-form label-width="96px">
        <el-form-item label="待合并账户">
          <el-checkbox-group v-model="mergeSourceIds" class="merge-dialog__list">
            <el-checkbox v-for="a in master.accounts" :key="a.id" :value="a.id">
              <span class="text-secondary">{{ a.display_name }}</span>
              <!-- GET /accounts 列表不返回 transaction_count，故不展示笔数（合并后由 account_details 给出） -->
            </el-checkbox>
          </el-checkbox-group>
        </el-form-item>
        <el-form-item label="目标账户名">
          <el-input v-model="mergeTargetName" maxlength="30" placeholder="总账户" />
        </el-form-item>
        <el-form-item label="源账户">
          <el-checkbox v-model="mergeDeleteSources">合并后删除源账户（推荐）</el-checkbox>
        </el-form-item>
      </el-form>
      </template>

      <template #footer>
        <template v-if="mergeResult">
          <el-button type="primary" @click="mergeOpen = false">完成</el-button>
        </template>
        <template v-else>
          <el-button @click="mergeOpen = false">取消</el-button>
          <el-button type="danger" :loading="merging" @click="confirmMerge">确认合并</el-button>
        </template>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
/**
 * 账户设置（T10 验收 2/3）：display_name 自动拼接、后四位限 4 位数字、初始余额元→分、
 * 上下箭头排序（POST /accounts/reorder）、409 ACCOUNT_IN_USE 引导归档、冻结账户 409 FROZEN 文案。
 * v1.2 R5 / §5.6：顶部「合并为总账户」入口（二次确认需输入 MERGE）。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { archiveAccount, createAccount, deleteAccount, mergeAccounts, reorderAccounts, updateAccount } from '@/api/accounts'
import { useMasterStore } from '@/stores/master'
import { formatCents, parseYuan, sanitizeYuanInput } from '@/utils/money'
import { errorText, ApiError } from '@/api/client'
import MoneyText from '@/components/common/MoneyText.vue'
import type { AccountDTO, AccountMergeResult } from '@/api/types'

const VALUE_TYPE_LABELS: Record<string, string> = {
  wallet: '零钱包',
  wealth: '理财',
  yuebao: '余额宝',
  current: '活期',
  fixed: '定期',
  other: '其他',
}

const master = useMasterStore()
const loading = ref(false)
const saving = ref(false)
const dialogOpen = ref(false)
const editing = ref<AccountDTO | null>(null)
const formRef = ref<FormInstance>()
const frozenLocked = ref(false)

// ---------- 账户合并（R5） ----------
const mergeOpen = ref(false)
const merging = ref(false)
const mergeSourceIds = ref<string[]>([])
const mergeTargetName = ref('总账户')
const mergeDeleteSources = ref(true)
/** 合并成功后的后端回执（含 account_details），用于展示合并前后余额。 */
const mergeResult = ref<AccountMergeResult | null>(null)

/**
 * 后端 account_details 里**也包含目标账户**（且该行的余额/笔数为零值），
 * 只把源账户列表展示出来，目标账户单独作为「并入目标」显示，避免误读成「目标余额为 0」。
 */
const mergeSourceDetails = computed(() =>
  (mergeResult.value?.account_details ?? []).filter(
    (d) => d.account_id !== mergeResult.value?.target_account_id,
  ),
)

const mergeTargetLabel = computed(() => {
  const id = mergeResult.value?.target_account_id
  const hit = (mergeResult.value?.account_details ?? []).find((d) => d.account_id === id)
  return hit?.name || master.accounts.find((a) => a.id === id)?.display_name || id || '总账户'
})

const form = reactive({
  name: '',
  alias: '',
  bank_name: '',
  card_tail: '',
  value_type: 'current',
  initialYuan: '',
  is_count_in_total: 1 as 0 | 1,
})

const rules: FormRules = {
  name: [{ required: true, message: '名称必填', trigger: 'blur' }],
  card_tail: [
    {
      validator: (_r, v: string, cb) => {
        if (!v || /^\d{1,4}$/.test(v)) cb()
        else cb(new Error('卡号后四位必须为 1-4 位数字'))
      },
      trigger: 'blur',
    },
  ],
}

const previewName = computed(() =>
  form.card_tail ? `${form.name || '账户名称'}（${form.card_tail}）` : form.name || '账户名称',
)

async function load(): Promise<void> {
  loading.value = true
  try {
    await master.loadAccounts()
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  editing.value = null
  frozenLocked.value = false
  Object.assign(form, {
    name: '',
    alias: '',
    bank_name: '',
    card_tail: '',
    value_type: 'current',
    initialYuan: '',
    is_count_in_total: 1,
  })
  dialogOpen.value = true
}

function openEdit(row: AccountDTO): void {
  editing.value = row
  frozenLocked.value = false
  Object.assign(form, {
    name: row.name,
    alias: row.alias,
    bank_name: row.bank_name,
    card_tail: row.card_tail,
    value_type: row.value_type,
    initialYuan: (row.initial_balance_cents / 100).toFixed(2),
    is_count_in_total: row.is_count_in_total,
  })
  dialogOpen.value = true
}

async function save(): Promise<void> {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  const cents = parseYuan(form.initialYuan || '0')
  if (cents === null) {
    ElMessage.warning('初始余额格式不正确')
    return
  }
  saving.value = true
  try {
    const payload = {
      name: form.name.trim(),
      alias: form.alias.trim(),
      bank_name: form.bank_name.trim(),
      card_tail: form.card_tail.trim(),
      value_type: form.value_type,
      initial_balance_cents: cents,
      is_count_in_total: form.is_count_in_total,
    }
    if (editing.value) {
      await updateAccount(editing.value.id, payload)
      ElMessage.success('账户已更新')
    } else {
      await createAccount(payload)
      ElMessage.success('账户已创建')
    }
    dialogOpen.value = false
    await load()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'FROZEN') {
      frozenLocked.value = true
      ElMessage.error(e.message || '该账户存在冻结区交易，初始余额不可修改（409 FROZEN）')
    } else {
      ElMessage.error(errorText(e))
    }
  } finally {
    saving.value = false
  }
}

async function toggleArchive(row: AccountDTO): Promise<void> {
  try {
    await archiveAccount(row.id, !row.is_archived)
    ElMessage.success(row.is_archived ? '已取消归档' : '已归档')
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}

async function remove(row: AccountDTO): Promise<void> {
  try {
    await ElMessageBox.confirm(`确定删除账户「${row.display_name}」？仅无任何交易时可删除。`, '删除账户', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await deleteAccount(row.id)
    ElMessage.success('账户已删除')
    await load()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'ACCOUNT_IN_USE') {
      try {
        await ElMessageBox.confirm('该账户已有交易，不能删除。是否改为「归档」？', '无法删除', {
          confirmButtonText: '归档',
          cancelButtonText: '取消',
          type: 'warning',
        })
        await archiveAccount(row.id, true)
        ElMessage.success('已归档')
        await load()
      } catch {
        /* 用户取消 */
      }
    } else {
      ElMessage.error(errorText(e))
    }
  }
}

/** 上下箭头排序 → POST /accounts/reorder。 */
async function move(index: number, dir: -1 | 1): Promise<void> {
  const list = [...master.accounts]
  const target = index + dir
  if (target < 0 || target >= list.length) return
  const tmp = list[index]
  list[index] = list[target]
  list[target] = tmp
  master.accounts = list
  try {
    await reorderAccounts(list.map((a, i) => ({ id: a.id, sort_order: i * 10 })))
    ElMessage.success('排序已保存')
  } catch (e) {
    ElMessage.error(`排序保存失败：${errorText(e)}`)
    await load()
  }
}

onMounted(load)

/** 打开合并对话框：默认全选所有账户（含归档）。 */
function openMerge(): void {
  mergeResult.value = null
  mergeSourceIds.value = master.accounts.map((a) => a.id)
  mergeTargetName.value = '总账户'
  mergeDeleteSources.value = true
  mergeOpen.value = true
}

/**
 * 二次确认（输入 MERGE 才放行）→ POST /api/accounts/merge。
 * 成功后刷新账户列表与总余额（服务端已 rematerialize）。
 */
async function confirmMerge(): Promise<void> {
  if (mergeSourceIds.value.length < 2) {
    ElMessage.warning('至少勾选 2 个账户才能合并')
    return
  }
  if (!mergeTargetName.value.trim()) {
    ElMessage.warning('请填写目标账户名称')
    return
  }
  try {
    await ElMessageBox.prompt(
      `将把 ${mergeSourceIds.value.length} 个账户的历史并入「${mergeTargetName.value.trim()}」。服务端会先备份数据，再改写交易与转账双边并全量重算余额，此操作不可撤销。请输入 MERGE 确认：`,
      '危险操作确认',
      {
        confirmButtonText: '执行合并',
        cancelButtonText: '取消',
        inputPlaceholder: 'MERGE',
        inputValidator: (v: string) => (v === 'MERGE' ? true : '请输入大写 MERGE 以确认'),
        type: 'warning',
      },
    )
  } catch {
    return
  }
  merging.value = true
  try {
    // 后端契约：target.id 为空串 → 按 name 查找/新建；value_type 空则默认 wallet
    const res = await mergeAccounts({
      source_ids: mergeSourceIds.value,
      target: { id: '', name: mergeTargetName.value.trim(), value_type: 'other' },
      delete_sources: mergeDeleteSources.value,
      confirm: true,
    })
    mergeResult.value = res
    ElMessage.success(
      `已合并 ${res.merged_accounts} 个账户、搬移 ${res.moved_transactions} 笔交易，余额已重算`,
    )
    await load()
  } catch (e) {
    ElMessage.error(`合并失败：${errorText(e)}`)
  } finally {
    merging.value = false
  }
}
</script>

<style scoped lang="scss">
.panel-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.merge-hint {
  margin-bottom: 12px;
}

.merge-dialog__list {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  max-height: 200px;
  overflow-y: auto;
}
</style>
