<template>
  <div class="sheet-wizard">
    <el-steps :active="state.step" finish-status="success" simple class="sheet-wizard__steps">
      <el-step title="选择文件" />
      <el-step title="选择工作表" />
      <el-step title="列映射" />
      <el-step title="预览校验" />
      <el-step title="提交结果" />
    </el-steps>

    <el-alert
      v-if="state.error"
      type="error"
      show-icon
      :closable="false"
      class="sheet-wizard__error"
      :title="`操作失败：${state.error}`"
    >
      <div class="text-tertiary">已选文件与映射状态已保留，可直接重试或回退修改。</div>
    </el-alert>

    <div v-loading="state.loading" element-loading-text="处理中，请稍候…" class="sheet-wizard__body">
      <FileUploadStep v-if="state.step === 0" :state="state" />
      <SheetPicker v-else-if="state.step === 1" :state="state" />
      <MappingStep v-else-if="state.step === 2" :state="state" />
      <PreviewStep v-else-if="state.step === 3" :state="state" />
      <ResultStep
        v-else
        :result="state.result"
        :preview="state.preview"
        :freeze-mode="state.freezeMode"
        @restart="restart"
      />
    </div>

    <div v-if="state.step < 4" class="sheet-wizard__footer">
      <el-button :disabled="state.step === 0 || state.loading" @click="prev">上一步</el-button>
      <el-button type="primary" :loading="state.loading" :disabled="!canNext" @click="next">
        {{ primaryText }}
      </el-button>
      <span v-if="blockedReason" class="sheet-wizard__hint">{{ blockedReason }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 表格导入向导壳（ARCHITECTURE-V12 §5.1 / T06）：
 * 步 1 选文件 → 步 2 选表 → 步 3 列映射 → 步 4 预览 → 步 5 结果。
 * 失败可回退，已选文件与映射状态全部保留（state 由本组件持有并透传给各步）。
 */
import { computed, onMounted, reactive } from 'vue'
import { ElMessage } from 'element-plus'
import { probeSheets, previewSheets, commitSheets } from '@/api/imports'
import { errorText } from '@/api/client'
import { useMasterStore } from '@/stores/master'
import { useMetaStore } from '@/stores/meta'
import { validateMapping } from '@/utils/sheetMapping'
import FileUploadStep from './FileUploadStep.vue'
import SheetPicker from './SheetPicker.vue'
import MappingStep from './MappingStep.vue'
import PreviewStep from './PreviewStep.vue'
import ResultStep from './ResultStep.vue'
import {
  buildCandidates,
  buildCommitReq,
  buildPreviewReq,
  commitButtonText,
  createWizardState,
  fingerprint,
  syncConfigs,
  type WizardState,
} from './types'

const master = useMasterStore()
const meta = useMetaStore()
const state = reactive<WizardState>(createWizardState())

onMounted(() => {
  if (!master.accounts.length) void master.loadAll()
  if (!state.accountId) state.accountId = meta.defaultAccountId ?? master.activeAccounts[0]?.id ?? null
})

const selectedSheets = computed(() => state.candidates.filter((c) => c.selected))
const invalidConfigs = computed(() =>
  state.configs.filter(
    (c) => validateMapping(c.mapping, { defaultDirection: c.directionDefault }).errors.length > 0,
  ),
)

const primaryText = computed(() => {
  if (state.step === 0) return '下一步：探测表格'
  if (state.step === 1) return '下一步：列映射'
  if (state.step === 2) return '下一步：生成预览'
  return commitButtonText(state.preview)
})

const blockedReason = computed(() => {
  if (state.loading) return ''
  if (state.step === 0 && !state.files.length) return '请至少选择一个 .xlsx / .xlsm / .csv 文件'
  if (state.step === 1 && !selectedSheets.value.length) return '请至少勾选一张工作表'
  if (state.step === 2 && invalidConfigs.value.length)
    return `${invalidConfigs.value.length} 张表缺少必填映射（日期 / 金额）`
  return ''
})

const canNext = computed(() => !state.loading && blockedReason.value === '')

/** 步进：每一步的副作用（探测 / 预览 / 提交）都集中在这里，失败不清状态。 */
async function next(): Promise<void> {
  state.error = ''
  if (state.step === 0) {
    await doProbe()
    return
  }
  if (state.step === 1) {
    if (!selectedSheets.value.length) {
      ElMessage.warning('请至少勾选一张工作表')
      return
    }
    state.configs = syncConfigs(state.candidates, state.configs, meta.precision)
    state.step = 2
    return
  }
  if (state.step === 2) {
    if (invalidConfigs.value.length) {
      ElMessage.warning('存在缺少必填映射的表，请先补齐日期与金额')
      return
    }
    await doPreview()
    return
  }
  if (state.step === 3) {
    if (state.preview && fingerprint(state.configs) !== state.previewFingerprint) {
      ElMessage.warning('映射已变更，请先重新生成预览')
      await doPreview()
      return
    }
    await doCommit()
  }
}

function prev(): void {
  if (state.step === 0) return
  state.error = ''
  state.step -= 1
}

async function doProbe(): Promise<void> {
  if (!state.files.length) {
    ElMessage.warning('请先选择至少一个文件')
    return
  }
  state.loading = true
  try {
    const probe = await probeSheets(state.files, state.accountId)
    state.probe = probe
    state.batchId = probe.batch_id
    state.candidates = buildCandidates(probe)
    state.configs = []
    state.preview = null
    state.result = null
    state.step = 1
    const sheetCount = state.candidates.length
    ElMessage.success(`探测完成：${probe.files.length} 个文件 / ${sheetCount} 张表`)
  } catch (e) {
    state.error = errorText(e)
  } finally {
    state.loading = false
  }
}

async function doPreview(): Promise<void> {
  state.loading = true
  try {
    const preview = await previewSheets(buildPreviewReq(state))
    state.preview = preview
    state.previewFingerprint = fingerprint(state.configs)
    state.step = 3
  } catch (e) {
    state.error = errorText(e)
  } finally {
    state.loading = false
  }
}

async function doCommit(): Promise<void> {
  state.loading = true
  try {
    const result = await commitSheets(buildCommitReq(state))
    state.result = result
    state.step = 4
    ElMessage.success(`导入完成：创建 ${result.created} 笔`)
  } catch (e) {
    state.error = errorText(e)
  } finally {
    state.loading = false
  }
}

/** 再导入一批：保留默认账户，其它清空。 */
function restart(): void {
  const accountId = state.accountId
  Object.assign(state, createWizardState())
  state.accountId = accountId
}
</script>

<style scoped lang="scss">
.sheet-wizard {
  &__steps {
    margin-bottom: 16px;
  }

  &__error {
    margin-bottom: 12px;
  }

  &__body {
    min-height: 220px;
  }

  &__footer {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-top: 16px;
    padding-top: 12px;
    border-top: 1px dashed var(--border-1);
  }

  &__hint {
    font-size: 12px;
    color: var(--text-3);
  }
}
</style>
