<template>
  <div class="result-step">
    <el-result icon="success" title="导入完成" :sub-title="subtitle">
      <template #extra>
        <div class="result-step__stats">
          <div class="result-step__stat">
            <div class="result-step__num amount-income">{{ result?.created ?? 0 }}</div>
            <div class="text-tertiary">已创建</div>
          </div>
          <div class="result-step__stat">
            <div class="result-step__num">{{ result?.skipped_dup ?? 0 }}</div>
            <div class="text-tertiary">跳过重复</div>
          </div>
          <div class="result-step__stat">
            <div class="result-step__num">{{ result?.skipped_soft_dup ?? 0 }}</div>
            <div class="text-tertiary">跳过软重复</div>
          </div>
          <div class="result-step__stat">
            <div class="result-step__num">{{ result?.skipped_invalid ?? 0 }}</div>
            <div class="text-tertiary">无效行</div>
          </div>
          <div class="result-step__stat">
            <div class="result-step__num">{{ result?.frozen_created ?? 0 }}</div>
            <div class="text-tertiary">含冻结区</div>
          </div>
        </div>

        <div v-if="createdChips.length" class="result-step__chips">
          <span class="text-tertiary">新建主数据：</span>
          <el-tag v-for="c in createdChips" :key="c" size="small" type="warning" effect="plain">{{ c }}</el-tag>
        </div>

        <!-- v1.2 R6：defer 模式下的待确认冻结闭环（批量确认 → 同步物化） -->
        <div v-if="pendingInitial > 0" class="result-step__pending">
          <el-alert
            v-if="!confirmedResult"
            type="warning"
            show-icon
            :closable="false"
            :title="pendingSummaryText(pendingInitial)"
          >
            <div class="text-tertiary">
              这些记录目前<b>仍可直接编辑</b>（未进冻结区）。核对无误后点右侧按钮一次确认，确认后即转为只读。
            </div>
            <el-button
              type="primary"
              size="small"
              :loading="confirming"
              style="margin-top: 8px"
              @click="confirmAll"
            >
              {{ confirming ? '确认中，正在重算余额…' : '全部确认冻结' }}
            </el-button>
          </el-alert>
          <el-alert
            v-else
            type="success"
            show-icon
            :closable="false"
            :title="`已确认冻结 ${confirmedResult.confirmed} 笔，剩余待确认 ${confirmedResult.pending_remaining} 笔`"
          >
            <div class="text-tertiary">
              <template v-if="confirmedResult.materialized">
                余额已重算 {{ confirmedResult.materialize_rows }} 行（{{ confirmedResult.materialize_elapsed_ms }}ms），
                冻结边界推进到 {{ confirmedResult.freeze_boundary }}
              </template>
              <template v-else>
                冻结状态已保存，但余额物化未完成{{ confirmedResult.materialize_error ? `（${confirmedResult.materialize_error}）` : '' }}，稍后系统会自动补偿重算
              </template>
              <template v-if="(confirmedResult.skipped_missing ?? 0) > 0">
                ；另有 {{ confirmedResult.skipped_missing }} 笔已确认或不存在，已跳过
              </template>
            </div>
            <el-button size="small" style="margin-top: 8px" @click="emit('restart')">再导入一批</el-button>
          </el-alert>
        </div>

        <!-- 立即冻结模式（或无待确认行）时的历史数据说明 -->
        <el-alert
          v-if="(result?.frozen_created ?? 0) > 0"
          type="warning"
          show-icon
          :closable="false"
          style="margin: 8px 0"
          :title="`其中 ${result?.frozen_created} 笔为历史记录，已进入冻结区（默认只读）`"
        >
          <div class="text-tertiary">
            如需修改请使用「调整分录」；可在「设置 → 系统 → 冻结天数」调整冻结范围（10–365 天）。
          </div>
        </el-alert>

        <el-alert
          v-if="result?.partial"
          type="warning"
          show-icon
          :closable="false"
          style="margin: 8px 0"
          title="部分分块写入失败（partial:true），已成功的数据已保留，可再次预览补导缺失区间"
        />
        <el-alert
          v-if="result?.time_distribution_shift_warning"
          type="info"
          show-icon
          :closable="false"
          style="margin: 8px 0"
          title="本次只导入了部分行，相关日期的同日时间分布已重新计算"
        />
        <el-alert
          v-if="result?.rematerialize_triggered"
          type="success"
          show-icon
          :closable="false"
          style="margin: 8px 0"
          title="余额已重算（rematerialize 已触发）"
        />

        <div class="result-step__actions">
          <el-button type="primary" @click="viewCreated">查看新交易</el-button>
          <el-button @click="emit('restart')">再导入一批</el-button>
        </div>

        <div class="text-tertiary" style="margin-top: 8px">
          批次号 {{ result?.batch_id }}
          <template v-if="result?.transaction_ids_omitted">（交易数超过 500，未回传逐笔 ID）</template>
        </div>
      </template>
    </el-result>
  </div>
</template>

<script setup lang="ts">
/**
 * 步 5：提交结果（ARCHITECTURE-V12 §5.1）：
 * created / skipped_* / frozen_created + 新建分类标签 + 「查看导入的交易」。
 */
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { confirmFreeze } from '@/api/transactions'
import { errorText } from '@/api/client'
import { pendingSummaryText, resolvePendingCount } from '@/utils/freeze'
import type { FreezeConfirmResult, FreezeMode, SheetCommitResult, SheetPreviewDTO } from '@/api/types'
import { resultDateRange } from './types'

const props = defineProps<{
  result: SheetCommitResult | null
  preview: SheetPreviewDTO | null
  /** 本次导入的冻结策略：immediate 时不显示待确认提示（T13 验收④）。 */
  freezeMode: FreezeMode
}>()
const emit = defineEmits<{ restart: [] }>()

const confirming = ref(false)
const confirmedResult = ref<FreezeConfirmResult | null>(null)

/** 待确认行数：优先 commit 回传的 pending_frozen，缺失则按预览 frozen_count 估计。 */
const pendingInitial = computed(() =>
  resolvePendingCount(props.freezeMode, props.result?.pending_frozen, props.preview?.summary.frozen_count ?? 0),
)

/**
 * 全部确认冻结：整批确认（batch_id 取本次 imp_xxx）。
 * 后端同步物化（6746 行实测约 1.3s，最长按 10s 超时处理），故给 loading 与禁用双保险。
 */
async function confirmAll(): Promise<void> {
  const batchId = props.result?.batch_id
  if (!batchId) {
    ElMessage.warning('缺少批次号，无法整批确认')
    return
  }
  confirming.value = true
  try {
    const res = await confirmFreeze({ batch_id: batchId, all: false, confirm: true })
    confirmedResult.value = res
    ElMessage.success(`已确认冻结 ${res.confirmed} 笔，剩余待确认 ${res.pending_remaining} 笔`)
  } catch (e) {
    ElMessage.error(`确认冻结失败：${errorText(e)}`)
  } finally {
    confirming.value = false
  }
}

const router = useRouter()

const subtitle = computed(() => {
  const r = props.result
  if (!r) return ''
  return `创建 ${r.created} 笔 · 跳过 ${r.skipped_dup + r.skipped_soft_dup} 笔 · 无效 ${r.skipped_invalid} 笔`
})

const createdChips = computed(() => {
  const r = props.result
  if (!r) return []
  return [
    ...r.created_categories.map((c) => `分类：${c.name}`),
    ...r.created_tags.map((t) => `标签：${t.name}`),
  ]
})

/**
 * 跳明细页并带上导入日期区间（§5.1 步 5）。
 * 明细页只识别 `start_date` / `end_date`（见 TransactionsView onMounted），故按该键名传参。
 */
function viewCreated(): void {
  const { from, to } = resultDateRange(props.preview)
  const query: Record<string, string> = { source: 'import' }
  if (from) query.start_date = from
  if (to) query.end_date = to
  void router.push({ path: '/transactions', query })
}
</script>

<style scoped lang="scss">
.result-step {
  &__stats {
    display: flex;
    gap: 24px;
    justify-content: center;
    flex-wrap: wrap;
    margin-bottom: 12px;
  }

  &__stat {
    text-align: center;
    min-width: 72px;
  }

  &__num {
    font-size: 22px;
    font-weight: 700;
    color: var(--text-1);
  }

  &__chips {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    justify-content: center;
    align-items: center;
    margin-bottom: 8px;
  }

  &__pending {
    margin: 8px 0;
    text-align: left;
  }

  &__actions {
    display: flex;
    gap: 12px;
    justify-content: center;
    margin-top: 8px;
  }
}
</style>
