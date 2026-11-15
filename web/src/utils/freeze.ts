/**
 * 导入延迟冻结的纯逻辑（v1.2 R6 / ARCHITECTURE-V12 §9.7）。
 * 抽成纯函数以便单测：默认策略、口径文案、计数与标签常量。
 */
import type { FreezeMode } from '@/api/types'

/** 默认策略：先核对再冻结（§9.7「默认 defer」）。 */
export const DEFAULT_FREEZE_MODE: FreezeMode = 'defer'

/** 「待确认冻结」标签文案：含「待确认」三字，与既有「冻结」刻意区分（§9.8）。 */
export const PENDING_TAG_TEXT = '待确认冻结'

export interface FreezeModeOption {
  value: FreezeMode
  label: string
  /** 选中后右侧说明（概述，不含数量）。 */
  description: string
}

export const FREEZE_MODE_OPTIONS: FreezeModeOption[] = [
  {
    value: 'defer',
    label: '先让我核对，确认后再冻结（推荐）',
    description: '这些记录保持可编辑，核对无误后再批量确认冻结',
  },
  {
    value: 'immediate',
    label: '导入后立即冻结',
    description: '导入即进入冻结区（只读），修改需走调整分录',
  },
]

/** 兜底：后端/本地存了脏值时收敛到 defer。 */
export function normalizeFreezeMode(v: unknown): FreezeMode {
  return v === 'immediate' ? 'immediate' : DEFAULT_FREEZE_MODE
}

/**
 * 第 4 步单选下方的实时说明（§9.7 验收①：切策略文案同步变）。
 * @param mode 当前策略
 * @param frozenCount 预览里的 `summary.frozen_count`（= 早于冻结天数、会进冻结区的行数）
 */
export function freezePolicyHint(mode: FreezeMode, frozenCount: number): string {
  const n = Number.isFinite(frozenCount) && frozenCount > 0 ? Math.trunc(frozenCount) : 0
  if (n === 0) {
    return '本次没有早于冻结天数的记录，无需处理冻结策略'
  }
  if (mode === 'immediate') {
    return `${n} 行将直接进入冻结区（只读），修改需使用调整分录`
  }
  return `其中 ${n} 行早于冻结天数的记录将标记为「${PENDING_TAG_TEXT}」，可随时修改`
}

/** 结果页/列表入口的待确认数量文案。 */
export function pendingSummaryText(pending: number): string {
  const n = Math.max(0, Math.trunc(pending || 0))
  return `${n} 笔历史数据待确认冻结`
}

/** 列表筛选入口按钮文案（0 时不显示，见调用处）。 */
export function pendingFilterButtonText(pending: number): string {
  return `${PENDING_TAG_TEXT} (${Math.max(0, Math.trunc(pending || 0))})`
}

/**
 * 待确认行数：优先用 commit 实际回传的 `pending_frozen`，
 * 缺失时按「defer 模式下预览的 frozen_count」估计；immediate 模式恒为 0。
 */
export function resolvePendingCount(
  mode: FreezeMode,
  commitPending: number | undefined | null,
  previewFrozenCount: number,
): number {
  if (mode === 'immediate') return 0
  if (typeof commitPending === 'number' && commitPending >= 0) return Math.trunc(commitPending)
  const fallback = Number.isFinite(previewFrozenCount) ? Math.trunc(previewFrozenCount) : 0
  return Math.max(0, fallback)
}
