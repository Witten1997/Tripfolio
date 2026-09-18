import { describe, expect, it } from 'vitest'

import type { LedgerEntry } from '@/shared/api/ledger'
import {
  changedLedgerFields,
  ledgerDraftFrom,
  validateLedgerDraft,
} from '@/shared/travel/ledgerDraft'

const cny = { currency: 'CNY', minorUnits: 2 }

const baseEntry: LedgerEntry = {
  id: 'e1',
  trip_id: 't',
  kind: 'expense',
  amount: '120.00',
  split_count: 1,
  personal_amount: '120.00',
  currency_code: 'CNY',
  category_id: 'c-food',
  occurred_on: '2026-10-02',
  notes: '午餐',
  refunded_entry_id: null,
  attachment_asset_ids: [],
  version: '1',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
  deleted_at: null,
}

describe('ledgerDraft', () => {
  it('草稿从账目还原，退款关联用 空串 表示未关联', () => {
    const draft = ledgerDraftFrom(baseEntry)
    expect(draft.kind).toBe('expense')
    expect(draft.amount).toBe('120.00')
    expect(draft.refunded_entry_id).toBe('')
  })

  it('校验：金额按币种规范化并始终携带币种，日期必填', () => {
    const draft = ledgerDraftFrom(baseEntry)
    draft.amount = '88.5'
    const values = validateLedgerDraft(draft, cny)
    expect(values.amount).toBe('88.50')
    expect(values.currency_code).toBe('CNY')
    expect(values.occurred_on).toBe('2026-10-02')
    expect(values.refunded_entry_id).toBeNull()
  })

  it('金额为空、为零、分类缺失、日期非法都被拦截', () => {
    const draft = ledgerDraftFrom(baseEntry)
    draft.amount = ''
    expect(() => validateLedgerDraft(draft, cny)).toThrow('金额')
    draft.amount = '0'
    expect(() => validateLedgerDraft(draft, cny)).toThrow('大于 0')
    draft.amount = '10'
    draft.category_id = ''
    expect(() => validateLedgerDraft(draft, cny)).toThrow('分类')
    draft.category_id = 'c-food'
    draft.occurred_on = '2026-13-40'
    expect(() => validateLedgerDraft(draft, cny)).toThrow('日期')
    draft.occurred_on = ''
    expect(() => validateLedgerDraft(draft, cny)).toThrow('日期')
  })

  it('JPY 无小数：带小数的金额被拒', () => {
    const draft = ledgerDraftFrom(baseEntry)
    draft.amount = '100.5'
    expect(() => validateLedgerDraft(draft, { currency: 'JPY', minorUnits: 0 })).toThrow('小数')
  })

  it('退款可关联原支出；支出即使填了关联也强制为空', () => {
    const refund = ledgerDraftFrom({ ...baseEntry, kind: 'refund' })
    refund.refunded_entry_id = 'e1'
    expect(validateLedgerDraft(refund, cny).refunded_entry_id).toBe('e1')
    const expense = ledgerDraftFrom(baseEntry)
    expense.refunded_entry_id = 'e1'
    expect(validateLedgerDraft(expense, cny).refunded_entry_id).toBeNull()
  })

  it('差异只含变化字段，改金额附带币种，kind 不进 PATCH', () => {
    const draft = ledgerDraftFrom(baseEntry)
    draft.amount = '200'
    draft.notes = '晚餐'
    const patch = changedLedgerFields(validateLedgerDraft(draft, cny), baseEntry)
    expect(patch).toEqual({ amount: '200.00', currency_code: 'CNY', notes: '晚餐' })
    expect('kind' in patch).toBe(false)
  })

  it('退款解除关联时发出显式 null', () => {
    const linked: LedgerEntry = { ...baseEntry, kind: 'refund', refunded_entry_id: 'e0' }
    const draft = ledgerDraftFrom(linked)
    draft.refunded_entry_id = ''
    const patch = changedLedgerFields(validateLedgerDraft(draft, cny), linked)
    expect(patch).toEqual({ refunded_entry_id: null })
  })
})
