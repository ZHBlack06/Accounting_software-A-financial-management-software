/**
 * 时间精度格式化单测（ARCHITECTURE-V12 §5.4 / T07 验收 ⑤）。
 */
import { describe, expect, it } from 'vitest'
import {
  applyPrecision,
  fmtBucketLabel,
  fmtFullTxnTime,
  fmtShort,
  fmtTxnTime,
} from './datetime'

const THIS_YEAR = new Date().getFullYear()

describe('fmtTxnTime', () => {
  it("precision='second' 输出完整年月日时分秒", () => {
    const v = `${THIS_YEAR}-03-05 14:07:09`
    expect(fmtTxnTime(v, 'second')).toBe(`${THIS_YEAR}-03-05 14:07:09`)
  })

  it("precision='second' 跨年同样带年份与秒", () => {
    expect(fmtTxnTime('2018-10-10 06:00:00', 'second')).toBe('2018-10-10 06:00:00')
  })

  it('fmtShort 仍保留紧凑格式（供备份等非交易场景使用）', () => {
    expect(fmtShort(`${THIS_YEAR}-03-05 14:07:09`)).toBe('03-05 14:07')
  })

  it("precision='day' 只显示 YYYY-MM-DD，不含时间分隔符与 00:00:00", () => {
    const out = fmtTxnTime('2018-10-10 06:00:00', 'day')
    expect(out).toBe('2018-10-10')
    expect(out).not.toContain(':')
    expect(out).not.toContain('00:00:00')
  })

  it('空值统一显示占位符', () => {
    expect(fmtTxnTime('', 'day')).toBe('—')
    expect(fmtTxnTime('', 'second')).toBe('—')
  })
})

describe('fmtFullTxnTime', () => {
  it("precision='second' 输出 YYYY-MM-DD HH:mm:ss", () => {
    expect(fmtFullTxnTime('2026-01-05 12:30:15', 'second')).toBe('2026-01-05 12:30:15')
  })

  it("precision='day' 输出 YYYY-MM-DD", () => {
    expect(fmtFullTxnTime('2026-01-05 12:30:15', 'day')).toBe('2026-01-05')
  })

  it('缺省精度为 second', () => {
    expect(fmtFullTxnTime('2026-01-05 12:30:15')).toBe('2026-01-05 12:30:15')
  })
})

describe('fmtBucketLabel（图表 tooltip）', () => {
  it('纯日期桶原样返回，不造假时间', () => {
    expect(fmtBucketLabel('2018-10-10', 'second')).toBe('2018-10-10')
    expect(fmtBucketLabel('2018-10', 'day')).toBe('2018-10')
  })

  it('带时分秒的桶在 day 精度下裁剪到日期', () => {
    expect(fmtBucketLabel('2018-10-10 06:00:00', 'day')).toBe('2018-10-10')
    expect(fmtBucketLabel('2018-10-10 06:00:00', 'second')).toBe('2018-10-10 06:00:00')
  })
})

describe('applyPrecision', () => {
  it("day 模式保留原有时分", () => {
    expect(applyPrecision('2026-01-05 12:30:15', 'day')).toBe('2026-01-05 12:30:15')
  })

  it('day 模式日期无时分时补 00:00:00', () => {
    expect(applyPrecision('2026-01-05', 'day')).toBe('2026-01-05 00:00:00')
  })

  it('second 模式原样返回', () => {
    expect(applyPrecision('2026-01-05', 'second')).toBe('2026-01-05')
  })
})
