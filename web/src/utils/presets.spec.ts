import { describe, it, expect } from 'vitest'
import dayjs from 'dayjs'
import isoWeek from 'dayjs/plugin/isoWeek'
import { resolvePreset, currentRange, suggestGranularity } from './presets'

dayjs.extend(isoWeek)

// 固定 "今天"：2026-10-01 是周四
const NOW = dayjs('2026-10-01 15:30:00')

describe('presets.resolvePreset', () => {
  it('this_week 从周一起（周四 → 本周一 09-28）', () => {
    const r = resolvePreset('this_week', NOW)
    expect(r?.start).toBe('2026-09-28 00:00:00')
    expect(r?.end).toBe('2026-10-01 23:59:59')
  })

  it('last_7d 为今天-6d 到今天', () => {
    const r = resolvePreset('last_7d', NOW)
    expect(r?.start).toBe('2026-09-25 00:00:00')
    expect(r?.end).toBe('2026-10-01 23:59:59')
  })

  it('last_30d 为今天-29d', () => {
    const r = resolvePreset('last_30d', NOW)
    expect(r?.start).toBe('2026-09-02 00:00:00')
  })

  it('last_2_weeks 为今天-13d', () => {
    const r = resolvePreset('last_2_weeks', NOW)
    expect(r?.start).toBe('2026-09-18 00:00:00')
  })

  it('last_150d 为今天-149d', () => {
    const r = resolvePreset('last_150d', NOW)
    expect(r?.start).toBe('2026-05-05 00:00:00')
  })

  it('this_month 为本月 1 日至今', () => {
    const r = resolvePreset('this_month', NOW)
    expect(r?.start).toBe('2026-10-01 00:00:00')
    expect(r?.end).toBe('2026-10-01 23:59:59')
  })

  it('last_month 为整月（9/1 00:00:00 ~ 9/30 23:59:59）', () => {
    const r = resolvePreset('last_month', NOW)
    expect(r?.start).toBe('2026-09-01 00:00:00')
    expect(r?.end).toBe('2026-09-30 23:59:59')
  })

  it('this_quarter 为季度首月 1 日至今', () => {
    const r = resolvePreset('this_quarter', NOW)
    expect(r?.start).toBe('2026-10-01 00:00:00')
    const r2 = resolvePreset('this_quarter', dayjs('2026-08-15 10:00:00'))
    expect(r2?.start).toBe('2026-07-01 00:00:00')
  })

  it('this_year 为 1 月 1 日至今', () => {
    const r = resolvePreset('this_year', NOW)
    expect(r?.start).toBe('2026-01-01 00:00:00')
  })

  it('last_12_months 为 11 个月前的 1 日至今', () => {
    const r = resolvePreset('last_12_months', NOW)
    expect(r?.start).toBe('2025-11-01 00:00:00')
  })

  it('custom 返回 null', () => {
    expect(resolvePreset('custom', NOW)).toBeNull()
  })
})

describe('presets.currentRange', () => {
  it('custom + customRange → 补全时分秒', () => {
    const r = currentRange('custom', ['2026-09-01', '2026-09-15'], NOW)
    expect(r.start).toBe('2026-09-01 00:00:00')
    expect(r.end).toBe('2026-09-15 23:59:59')
  })

  it('custom 无区间 → 回退 last_30d', () => {
    const r = currentRange('custom', null, NOW)
    expect(r.start).toBe('2026-09-02 00:00:00')
  })
})

describe('presets.suggestGranularity', () => {
  it('≤92 天用 day，>92 用 week，再长用 month/year', () => {
    expect(suggestGranularity({ start: '2026-09-02 00:00:00', end: '2026-10-01 23:59:59' })).toBe('day')
    expect(suggestGranularity({ start: '2026-01-01 00:00:00', end: '2026-10-01 23:59:59' })).toBe('week')
    expect(suggestGranularity({ start: '2024-01-01 00:00:00', end: '2026-10-01 23:59:59' })).toBe('month')
    expect(suggestGranularity({ start: '2020-01-01 00:00:00', end: '2026-10-01 23:59:59' })).toBe('year')
  })
})
