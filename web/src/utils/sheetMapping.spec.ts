/**
 * 表格导入列映射校验单测（ARCHITECTURE-V12 §5.3 / T06 验收 ②）+ `.xls` 拦截文案。
 */
import { describe, expect, it } from 'vitest'
import {
  ACCEPT_EXT,
  applyMappingByColumnName,
  checkSheetFile,
  emptyMapping,
  formatSize,
  normalizeMapping,
  pickPrecision,
  validateMapping,
  XLS_HINT,
} from './sheetMapping'

describe('validateMapping', () => {
  it('缺日期与金额 → 两条必填错误', () => {
    const res = validateMapping(emptyMapping())
    expect(res.errors.length).toBe(2)
    expect(res.missing).toEqual(['日期', '金额'])
    expect(res.errors.join('|')).toContain('日期')
    expect(res.errors.join('|')).toContain('金额')
  })

  it('缺金额（有日期）→ 只报金额', () => {
    const m = { ...emptyMapping(), date: '日期' }
    const res = validateMapping(m)
    expect(res.missing).toEqual(['金额'])
    expect(res.errors).toEqual(['请映射「金额」列'])
  })

  it('缺日期（有金额）→ 只报日期', () => {
    const m = { ...emptyMapping(), amount: '金额' }
    const res = validateMapping(m)
    expect(res.missing).toEqual(['日期'])
    expect(res.errors).toEqual(['请映射「日期」或「年份月」列'])
  })

  it('日期 + 金额齐备 → 无错误', () => {
    const m = { ...emptyMapping(), date: '日期', amount: '金额', category: '分类' }
    const res = validateMapping(m)
    expect(res.errors).toEqual([])
    expect(res.missing).toEqual([])
  })

  it('只有「年份月」列时可用但给出提醒', () => {
    const m = { ...emptyMapping(), month: '月份', amount: '金额' }
    const res = validateMapping(m)
    expect(res.errors).toEqual([])
    expect(res.warnings.join('|')).toContain('当月 1 日')
  })

  it('缺方向列时提示按表默认方向', () => {
    const m = { ...emptyMapping(), date: '日期', amount: '金额' }
    const res = validateMapping(m, { defaultDirection: 'income' })
    expect(res.warnings.join('|')).toContain('收入')
  })

  it('同一列被重复映射 → 报冲突', () => {
    const m = { ...emptyMapping(), date: '日期', amount: '日期' }
    const res = validateMapping(m)
    expect(res.errors.some((e) => e.includes('同时被映射'))).toBe(true)
  })
})

describe('checkSheetFile（.xls 拦截）', () => {
  it('.xls 被拒并提示另存为', () => {
    const res = checkSheetFile('记帐.xls', 1024)
    expect(res.ok).toBe(false)
    expect(res.reason).toContain('请另存为 .xlsx 或 .csv')
    expect(res.reason).toContain('记帐.xls')
    expect(XLS_HINT).toContain('请另存为 .xlsx 或 .csv')
  })

  it('.xlsx / .xlsm / .csv 放行', () => {
    expect(checkSheetFile('记帐.xlsx', 1024).ok).toBe(true)
    expect(checkSheetFile('记帐.XLSM', 1024).ok).toBe(true)
    expect(checkSheetFile('账单.csv', 1024).ok).toBe(true)
  })

  it('超过 30MB 被拒', () => {
    const res = checkSheetFile('big.xlsx', 31 * 1024 * 1024)
    expect(res.ok).toBe(false)
    expect(res.reason).toContain('30MB')
  })

  it('其它扩展名被拒，且 accept 不含 .xls', () => {
    const res = checkSheetFile('账单.pdf', 10)
    expect(res.ok).toBe(false)
    // accept 是逗号分隔的扩展名列表，需精确比对（'.xlsm' 里也含 '.xls' 子串）
    expect(ACCEPT_EXT.split(',')).not.toContain('.xls')
  })
})

describe('normalizeMapping', () => {
  it('补齐缺失字段并把空串归一为 null', () => {
    const m = normalizeMapping({ date: '日期', amount: '  ' })
    expect(m.date).toBe('日期')
    expect(m.amount).toBeNull()
    expect(m.tag).toBeNull()
  })
})

describe('applyMappingByColumnName', () => {
  it('按列名相同批量套用', () => {
    const source = { ...emptyMapping(), date: '日期', amount: '金额', category: '分类' }
    const { mapping, applied } = applyMappingByColumnName(source, ['日期', '金额', '备注'])
    expect(mapping.date).toBe('日期')
    expect(mapping.amount).toBe('金额')
    expect(mapping.category).toBeNull()
    expect(mapping.remark).toBeNull()
    expect(applied.sort()).toEqual(['amount', 'date'])
  })
})

describe('pickPrecision / formatSize', () => {
  it('源数据无时间 → 强制 day', () => {
    expect(pickPrecision(false, 'second')).toBe('day')
    expect(pickPrecision(true, 'second')).toBe('second')
  })

  it('体积格式化', () => {
    expect(formatSize(2048)).toBe('2.0 KB')
    expect(formatSize(3 * 1024 * 1024)).toBe('3.00 MB')
  })
})
