/**
 * 金额工具（ARCHITECTURE §10.5）：传输/存储一律分（cents），仅展示与输入解析两处做转换。
 */

/** 分 → "¥1,234.56"；负数输出 "-¥90.00"。 */
export function formatCents(cents: number): string {
  const sign = cents < 0 ? '-' : ''
  const abs = Math.abs(Math.trunc(cents))
  const yuan = Math.floor(abs / 100)
  const cent = abs % 100
  const yuanStr = yuan.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${sign}¥${yuanStr}.${String(cent).padStart(2, '0')}`
}

/** 分 → 元数值（保留两位小数的 number），仅用于图表轴等数值场景。 */
export function centsToYuan(cents: number): number {
  return Math.round(cents) / 100
}

/** 分 → 不带货币符号的元字符串 "1,234.56"。 */
export function formatCentsPlain(cents: number): string {
  return formatCents(cents).replace('¥', '')
}

/**
 * 用户输入元字符串 → 分。支持 "¥1,234.56"、"1234.56"、"-90"。
 * 非法输入返回 null。解析规则：`Math.round(parseFloat(s) * 100)`。
 */
export function parseYuan(input: string | null | undefined): number | null {
  if (input === null || input === undefined) return null
  const s = String(input).replace(/[¥￥\s,]/g, '')
  if (s === '' || s === '-' || s === '.') return null
  if (!/^-?\d+(\.\d{1,2})?$/.test(s)) return null
  const n = parseFloat(s)
  if (!Number.isFinite(n)) return null
  return Math.round(n * 100)
}

/** 金额输入框过滤：只保留数字与一个小数点，最多两位小数。 */
export function sanitizeYuanInput(raw: string): string {
  let s = raw.replace(/[^\d.]/g, '')
  const firstDot = s.indexOf('.')
  if (firstDot >= 0) {
    s = s.slice(0, firstDot + 1) + s.slice(firstDot + 1).replace(/\./g, '')
    const [intPart, decPart] = s.split('.')
    s = decPart !== undefined ? `${intPart}.${decPart.slice(0, 2)}` : intPart
  }
  return s
}

/** 带符号展示：支出 -¥x、收入 +¥x、转账 ¥x（无符号）。 */
export function formatSigned(cents: number, type: 'expense' | 'income' | 'transfer'): string {
  if (type === 'expense') return `-${formatCents(Math.abs(cents))}`
  if (type === 'income') return `+${formatCents(Math.abs(cents))}`
  return formatCents(Math.abs(cents))
}
