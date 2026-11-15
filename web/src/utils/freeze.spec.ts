/**
 * 导入延迟冻结纯逻辑单测（v1.2 R6 / §9.7 / T13 验收 ①）。
 */
import { describe, expect, it } from 'vitest'
import {
  DEFAULT_FREEZE_MODE,
  FREEZE_MODE_OPTIONS,
  PENDING_TAG_TEXT,
  freezePolicyHint,
  normalizeFreezeMode,
  pendingFilterButtonText,
  pendingSummaryText,
  resolvePendingCount,
} from './freeze'

describe('默认策略', () => {
  it('默认必须是 defer（先核对再冻结）', () => {
    expect(DEFAULT_FREEZE_MODE).toBe('defer')
  })

  it('脏值收敛到 defer，immediate 原样保留', () => {
    expect(normalizeFreezeMode(undefined)).toBe('defer')
    expect(normalizeFreezeMode('weird')).toBe('defer')
    expect(normalizeFreezeMode('immediate')).toBe('immediate')
    expect(normalizeFreezeMode('defer')).toBe('defer')
  })

  it('两个选项都存在且 defer 排第一（推荐项在前）', () => {
    expect(FREEZE_MODE_OPTIONS.map((o) => o.value)).toEqual(['defer', 'immediate'])
    expect(FREEZE_MODE_OPTIONS[0].label).toContain('推荐')
  })
})

describe('freezePolicyHint 文案随策略切换', () => {
  it('defer：提示将标记为待确认冻结、可修改', () => {
    const t = freezePolicyHint('defer', 6300)
    expect(t).toContain('6300')
    expect(t).toContain(PENDING_TAG_TEXT)
    expect(t).toContain('可随时修改')
  })

  it('immediate：提示直接进入冻结区、只读', () => {
    const t = freezePolicyHint('immediate', 6300)
    expect(t).toContain('6300')
    expect(t).toContain('只读')
    expect(t).not.toContain(PENDING_TAG_TEXT)
  })

  it('两种策略文案必须不同', () => {
    expect(freezePolicyHint('defer', 42)).not.toBe(freezePolicyHint('immediate', 42))
  })

  it('没有早于边界的行时给出中性说明', () => {
    expect(freezePolicyHint('defer', 0)).toContain('无需处理冻结策略')
    expect(freezePolicyHint('immediate', -1)).toContain('无需处理冻结策略')
  })

  it('非法/NaN 计数按 0 处理', () => {
    expect(freezePolicyHint('defer', Number.NaN)).toContain('无需处理冻结策略')
  })
})

describe('待确认数量与文案', () => {
  it('结果页 / 筛选按钮文案', () => {
    expect(pendingSummaryText(6746)).toBe('6746 笔历史数据待确认冻结')
    expect(pendingFilterButtonText(12)).toBe(`${PENDING_TAG_TEXT} (12)`)
    expect(pendingFilterButtonText(-3)).toBe(`${PENDING_TAG_TEXT} (0)`)
  })

  it('immediate 模式恒为 0，即使后端误传', () => {
    expect(resolvePendingCount('immediate', 100, 6300)).toBe(0)
  })

  it('defer：优先用 commit 回传的 pending_frozen', () => {
    expect(resolvePendingCount('defer', 6300, 6746)).toBe(6300)
  })

  it('defer：commit 未回传时按预览 frozen_count 估计', () => {
    expect(resolvePendingCount('defer', undefined, 6300)).toBe(6300)
    expect(resolvePendingCount('defer', null, Number.NaN)).toBe(0)
  })
})
