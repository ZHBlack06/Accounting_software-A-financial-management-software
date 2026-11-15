import { describe, it, expect } from 'vitest'
import { formatCents, parseYuan, sanitizeYuanInput, formatSigned, centsToYuan } from './money'

describe('money.formatCents', () => {
  it('负数带符号', () => {
    expect(formatCents(-9000)).toBe('-¥90.00')
  })
  it('千分位', () => {
    expect(formatCents(123456)).toBe('¥1,234.56')
  })
  it('零与个位', () => {
    expect(formatCents(0)).toBe('¥0.00')
    expect(formatCents(5)).toBe('¥0.05')
  })
  it('大额', () => {
    expect(formatCents(2135700)).toBe('¥21,357.00')
  })
})

describe('money.parseYuan', () => {
  it('带 ¥ 与千分位', () => {
    expect(parseYuan('¥1,234.56')).toBe(123456)
  })
  it('纯数字与小数', () => {
    expect(parseYuan('100.00')).toBe(10000)
    expect(parseYuan('90')).toBe(9000)
  })
  it('负数', () => {
    expect(parseYuan('-90')).toBe(-9000)
  })
  it('非法输入返回 null', () => {
    expect(parseYuan('')).toBeNull()
    expect(parseYuan('abc')).toBeNull()
    expect(parseYuan('1.2.3')).toBeNull()
    expect(parseYuan(null)).toBeNull()
    expect(parseYuan('--')).toBeNull()
  })
})

describe('money.sanitizeYuanInput', () => {
  it('只保留数字与一个小数点，最多两位小数', () => {
    expect(sanitizeYuanInput('12a3.4.56')).toBe('123.45')
    expect(sanitizeYuanInput('abc')).toBe('')
    expect(sanitizeYuanInput('1.234')).toBe('1.23')
  })
})

describe('money.formatSigned / centsToYuan', () => {
  it('支出负号、收入正号、转账无符号', () => {
    expect(formatSigned(9000, 'expense')).toBe('-¥90.00')
    expect(formatSigned(9000, 'income')).toBe('+¥90.00')
    expect(formatSigned(9000, 'transfer')).toBe('¥90.00')
  })
  it('centsToYuan', () => {
    expect(centsToYuan(123456)).toBe(1234.56)
  })
})
