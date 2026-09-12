export type MoneyErrorCode = 'INVALID_FORMAT' | 'SCALE_EXCEEDED' | 'NEGATIVE' | 'TOO_LARGE'

export class MoneyError extends Error {
  constructor(
    readonly code: MoneyErrorCode,
    message: string,
  ) {
    super(message)
    this.name = 'MoneyError'
  }
}

/** NUMERIC(18,4) 允许的最大整数位数。 */
export const MAX_INTEGER_DIGITS = 14

// 只接受 ASCII 数字，不接受符号、分组符、空白、科学计数法与全角数字
const UNSIGNED_DECIMAL = /^[0-9]+(\.[0-9]+)?$/

/**
 * 与服务端 foundation/money 相同的规范化规则：去掉整数部分前导零，小数补零到 minorUnits 位，
 * 小数位超过 minorUnits 一律拒绝（包括多余的 0）。测试样例来自 @tripfolio/contracts/fixtures/money.json。
 */
export function canonicalizeAmount(input: string, minorUnits: number): string {
  if (!Number.isInteger(minorUnits) || minorUnits < 0 || minorUnits > 4) {
    throw new RangeError('minorUnits must be an integer between 0 and 4')
  }
  if (input.startsWith('-') && UNSIGNED_DECIMAL.test(input.slice(1))) {
    throw new MoneyError('NEGATIVE', '金额不能为负数')
  }
  if (!UNSIGNED_DECIMAL.test(input)) {
    throw new MoneyError('INVALID_FORMAT', '金额格式不正确')
  }

  const [integerRaw = '', fraction = ''] = input.split('.')
  if (fraction.length > minorUnits) {
    throw new MoneyError('SCALE_EXCEEDED', `小数位不能超过 ${minorUnits} 位`)
  }
  const integer = integerRaw.replace(/^0+(?=\d)/, '')
  if (integer.length > MAX_INTEGER_DIGITS) {
    throw new MoneyError('TOO_LARGE', '金额超出可记录范围')
  }
  if (minorUnits === 0) return integer
  return `${integer}.${fraction.padEnd(minorUnits, '0')}`
}
