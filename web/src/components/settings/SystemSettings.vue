<template>
  <div class="system-settings">
    <el-form v-loading="loading" label-width="130px" style="max-width: 560px">
      <el-form-item label="时间显示">
        <div style="width: 100%">
          <el-radio-group :model-value="meta.timePrecision" :disabled="precisionSaving" @update:model-value="onPrecisionChange">
            <el-radio-button value="second">年月日时分秒（精确到秒）</el-radio-button>
            <el-radio-button value="day">年月日（仅日期）</el-radio-button>
          </el-radio-group>
          <div class="text-tertiary" style="margin-top: 6px">
            即时生效：切换后列表、详情、首页与报表的时间立即按所选精度显示；只影响展示，不修改已存数据。
          </div>
          <div class="precision-preview">
            <span class="text-tertiary">效果预览</span>
            <span class="precision-preview__value">{{ precisionPreview }}</span>
          </div>
        </div>
      </el-form-item>
      <el-form-item label="冻结天数">
        <el-input-number v-model="form.freezeDays" :min="10" :max="365" :step="5" />
        <span class="text-tertiary" style="margin-left: 8px">10–365 天，步长 5</span>
      </el-form-item>
      <el-form-item label="高额消费阈值">
        <el-input v-model="form.thresholdYuan" style="width: 140px" @input="form.thresholdYuan = sanitizeYuanInput(form.thresholdYuan)">
          <template #prefix>¥</template>
        </el-input>
        <span class="text-tertiary" style="margin-left: 8px">低于自动 P95 时使用该配置值</span>
      </el-form-item>
      <el-form-item label="默认账户">
        <el-select v-model="form.defaultAccountId" clearable style="width: 100%">
          <el-option v-for="a in master.activeAccounts" :key="a.id" :value="a.id" :label="a.display_name" />
        </el-select>
      </el-form-item>
      <el-form-item label="默认支出分类">
        <el-cascader
          v-model="defaultExpensePath"
          :options="expenseOptions"
          :props="{ checkStrictly: true }"
          clearable
          style="width: 100%"
        />
      </el-form-item>
      <el-form-item label="深色模式">
        <el-switch :model-value="ui.isDark" @change="ui.toggleTheme()" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="saving" @click="save">保存设置</el-button>
        <span class="text-tertiary" style="margin-left: 12px">修改冻结天数后次日 03:00 生效，本次会立即重算一次</span>
      </el-form-item>
    </el-form>

    <div class="templates">
      <div class="templates__head">
        <span class="templates__title">快捷记账模板</span>
        <el-button size="small" @click="addTemplate">＋新增模板</el-button>
      </div>
      <el-table :data="templates" size="small" empty-text="暂无模板（快速记账将展示内置模板）">
        <el-table-column label="名称" min-width="110">
          <template #default="{ row }">
            <el-input v-model="row.name" size="small" maxlength="10" @change="markDirty" />
          </template>
        </el-table-column>
        <el-table-column label="金额(元)" width="120">
          <template #default="{ row }">
            <el-input v-model="row.amountYuan" size="small" @input="row.amountYuan = sanitizeYuanInput(row.amountYuan); markDirty()" />
          </template>
        </el-table-column>
        <el-table-column label="排序" width="110" align="center">
          <template #default="{ $index }">
            <el-button link size="small" :disabled="$index === 0" @click="moveTemplate($index, -1)">↑</el-button>
            <el-button link size="small" :disabled="$index === templates.length - 1" @click="moveTemplate($index, 1)">↓</el-button>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="80">
          <template #default="{ $index }">
            <el-button link size="small" type="danger" @click="templates.splice($index, 1); markDirty()">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div style="margin-top: 8px">
        <el-button size="small" type="primary" :loading="saving" :disabled="!dirty" @click="saveTemplates">
          保存模板
        </el-button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 系统设置（T10 验收 8 / v1.2 T07）：冻结天数 10–365 步长 5（含生效提示）、高额阈值、默认账户/分类、
 * 记录时间精度（仅到日 / 精确到秒，写 settings.time_precision）、深色模式、
 * 快捷模板增删改排序（settings.quick_templates）。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { fetchSettings, updateSettings } from '@/api/meta'
import { fmtFullTxnTime } from '@/utils/datetime'
import { useMasterStore } from '@/stores/master'
import { useMetaStore } from '@/stores/meta'
import { useUiStore } from '@/stores/ui'
import { parseYuan, sanitizeYuanInput } from '@/utils/money'
import { errorText } from '@/api/client'
import type { QuickTemplate, TimePrecision } from '@/api/types'

interface TemplateRow extends QuickTemplate {
  amountYuan: string
}

const master = useMasterStore()
const ui = useUiStore()
const meta = useMetaStore()
const loading = ref(false)
const saving = ref(false)
const precisionSaving = ref(false)
const dirty = ref(false)
const templates = ref<TemplateRow[]>([])

const form = reactive({
  freezeDays: 30,
  thresholdYuan: '500.00',
  defaultAccountId: null as string | null,
  defaultCategoryExpenseId: null as string | null,
})

const defaultExpensePath = ref<string[] | null>(null)

/** 效果预览：用当前时刻按所选精度渲染，让用户切换前就能看到差别。 */
const precisionPreview = computed(() => {
  const now = new Date()
  const pad = (n: number): string => String(n).padStart(2, '0')
  const sample =
    `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())} ` +
    `${pad(now.getHours())}:${pad(now.getMinutes())}:${pad(now.getSeconds())}`
  return fmtFullTxnTime(sample, meta.timePrecision)
})

const expenseOptions = computed(() =>
  master.expenseCategories.map((c) => ({
    value: c.id,
    label: c.name,
    children: (c.children ?? []).map((s) => ({ value: s.id, label: s.name })),
  })),
)

async function load(): Promise<void> {
  loading.value = true
  try {
    const s = await fetchSettings()
    form.freezeDays = s.freeze_days
    form.thresholdYuan = (s.high_amount_threshold_cents / 100).toFixed(2)
    form.defaultAccountId = s.default_account_id
    form.defaultCategoryExpenseId = s.default_category_expense_id
    defaultExpensePath.value = s.default_category_expense_id ? [s.default_category_expense_id] : null
    templates.value = (s.quick_templates ?? []).map((t) => ({
      ...t,
      amountYuan: t.amount_cents != null ? (t.amount_cents / 100).toFixed(2) : '',
    }))
    if (s.time_precision) meta.setLocalPrecision(s.time_precision)
    dirty.value = false
  } catch (e) {
    ElMessage.error(`设置加载失败：${errorText(e)}`)
  } finally {
    loading.value = false
  }
}

/** v1.2 T07：切换记录时间精度（PUT /settings），成功后全站立即按新精度展示。 */
async function onPrecisionChange(v: string | number | boolean | undefined): Promise<void> {
  const next: TimePrecision = v === 'day' ? 'day' : 'second'
  if (next === meta.timePrecision) return
  precisionSaving.value = true
  try {
    await meta.setTimePrecision(next)
    ElMessage.success(next === 'day' ? '已切换为「仅到日」，列表与详情只显示日期' : '已切换为「精确到秒」')
  } catch (e) {
    ElMessage.error(`精度保存失败：${errorText(e)}`)
  } finally {
    precisionSaving.value = false
  }
}

async function save(): Promise<void> {
  const cents = parseYuan(form.thresholdYuan)
  if (cents === null || cents < 0) {
    ElMessage.warning('高额阈值格式不正确')
    return
  }
  saving.value = true
  try {
    await updateSettings({
      freeze_days: form.freezeDays,
      high_amount_threshold_cents: cents,
      default_account_id: form.defaultAccountId,
      default_category_expense_id: defaultExpensePath.value?.[0] ?? null,
    })
    ElMessage.success('设置已保存（冻结天数改动次日 03:00 生效，本次已重算）')
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    saving.value = false
  }
}

function markDirty(): void {
  dirty.value = true
}

function addTemplate(): void {
  templates.value.push({
    id: `tpl_${Date.now().toString(36)}`,
    name: '',
    amount_cents: null,
    account_id: null,
    category_id: null,
    merchant_id: null,
    sort_order: templates.value.length,
    amountYuan: '',
  })
  markDirty()
}

function moveTemplate(index: number, dir: -1 | 1): void {
  const target = index + dir
  if (target < 0 || target >= templates.value.length) return
  const tmp = templates.value[index]
  templates.value[index] = templates.value[target]
  templates.value[target] = tmp
  markDirty()
}

async function saveTemplates(): Promise<void> {
  const invalid = templates.value.find((t) => !t.name.trim())
  if (invalid) {
    ElMessage.warning('模板名称不能为空')
    return
  }
  saving.value = true
  try {
    const payload: QuickTemplate[] = templates.value.map((t, i) => ({
      id: t.id,
      name: t.name.trim(),
      amount_cents: t.amountYuan ? parseYuan(t.amountYuan) : null,
      account_id: t.account_id,
      category_id: t.category_id,
      merchant_id: t.merchant_id,
      sort_order: i,
    }))
    await updateSettings({ quick_templates: payload })
    dirty.value = false
    ElMessage.success('模板已保存')
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped lang="scss">
.precision-preview {
  margin-top: 10px;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  border: 1px dashed var(--border-1);
  border-radius: 6px;
  background: var(--bg-2);

  &__value {
    font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
    font-variant-numeric: tabular-nums;
    color: var(--text-1);
  }
}

.templates {
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px dashed var(--border-1);

  &__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
  }

  &__title {
    font-size: 14px;
    font-weight: 600;
    color: var(--text-1);
  }
}
</style>
