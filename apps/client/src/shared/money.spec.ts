import { describe, expect, it } from 'vitest'

import fixture from '@tripfolio/contracts/fixtures/money.json'

import { MoneyError, canonicalizeAmount } from './money'

describe('canonicalizeAmount 与共享样例一致', () => {
  for (const c of fixture.cases) {
    it(c.id, () => {
      if ('canonical' in c && typeof c.canonical === 'string') {
        expect(canonicalizeAmount(c.input, c.minor_units)).toBe(c.canonical)
        return
      }
      let caught: unknown
      try {
        canonicalizeAmount(c.input, c.minor_units)
      } catch (error) {
        caught = error
      }
      expect(caught).toBeInstanceOf(MoneyError)
      expect((caught as MoneyError).code).toBe(c.error)
    })
  }
})

describe('canonicalizeAmount 参数校验', () => {
  it('拒绝不支持的小数位数', () => {
    expect(() => canonicalizeAmount('1', 5)).toThrow(RangeError)
  })
})
