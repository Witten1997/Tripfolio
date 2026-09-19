import { describe, expect, it } from 'vitest'

import type { LedgerEntry } from '@/shared/api/ledger'
import type { TripMember } from '@/shared/api/members'
import {
  changedLedgerFields,
  defaultMemberDraft,
  ledgerDraftFrom,
  splitPreview,
  validateLedgerDraft,
} from '@/shared/travel/ledgerDraft'

function member(overrides: Partial<TripMember>): TripMember {
  return {
    id: 'm',
    trip_id: 't',
    name: '成员',
    share_percent: '0',
    sort_order: 0,
    is_self: false,
    version: '1',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    deleted_at: null,
    ...overrides,
  }
}

const cny = { currency: 'CNY', minorUnits: 2 }

const baseEntry: LedgerEntry = {
  id: 'e1',
  trip_id: 't',
  kind: 'expense',
  amount: '120.00',
  split_count: 1,
  personal_amount: '120.00',
  payer_member_id: 'm-self',
  split_mode: 'even',
  splits: [{ member_id: 'm-self', amount: '120.00' }],
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

  it('差异只含变化字段，改金额附带币种，kind 不进 PATCH，参与人未变不发送', () => {
    const draft = ledgerDraftFrom(baseEntry)
    draft.amount = '200'
    draft.notes = '晚餐'
    const patch = changedLedgerFields(validateLedgerDraft(draft, cny), baseEntry)
    expect(patch).toEqual({ amount: '200.00', currency_code: 'CNY', notes: '晚餐' })
    expect('kind' in patch).toBe(false)
    expect('participant_member_ids' in patch).toBe(false)
  })

  it('参与人、付款人或模式变化时进入 PATCH；参与人整体替换', () => {
    const draft = ledgerDraftFrom(baseEntry)
    draft.payer_member_id = 'm-b'
    draft.split_mode = 'ratio'
    draft.participant_member_ids = ['m-b', 'm-self']
    const patch = changedLedgerFields(validateLedgerDraft(draft, cny), baseEntry)
    expect(patch).toEqual({
      payer_member_id: 'm-b',
      split_mode: 'ratio',
      participant_member_ids: ['m-b', 'm-self'],
    })
  })

  it('付款人缺失或参与人为空被拦截；重复参与人去重', () => {
    const draft = ledgerDraftFrom(baseEntry)
    draft.payer_member_id = ''
    expect(() => validateLedgerDraft(draft, cny)).toThrow('付款人')
    draft.payer_member_id = 'm-self'
    draft.participant_member_ids = []
    expect(() => validateLedgerDraft(draft, cny)).toThrow('参与人')
    draft.participant_member_ids = ['m-self', 'm-self']
    expect(validateLedgerDraft(draft, cny).participant_member_ids).toEqual(['m-self'])
  })

  it('新建默认付款人为「我」、参与人为全员', () => {
    const members = [
      member({ id: 'm-b', name: '小王', is_self: false }),
      member({ id: 'm-self', name: '我', is_self: true }),
    ]
    expect(defaultMemberDraft(members)).toEqual({
      payer_member_id: 'm-self',
      participant_member_ids: ['m-b', 'm-self'],
    })
  })

  it('分摊预览与服务端算法一致：向下取整，余数按顺序补齐；按比例归一化', () => {
    const a = member({ id: 'a', share_percent: '50' })
    const b = member({ id: 'b', share_percent: '30' })
    const c = member({ id: 'c', share_percent: '20' })
    expect([...splitPreview('100', 'even', [a, b, c], 0)!.values()]).toEqual(['34', '33', '33'])
    expect([...splitPreview('100', 'ratio', [a, b, c], 0)!.values()]).toEqual(['50', '30', '20'])
    expect([...splitPreview('101', 'ratio', [c, b], 0)!.values()]).toEqual(['41', '60'])
    expect([...splitPreview('10', 'even', [a, b, c], 2)!.values()]).toEqual([
      '3.34',
      '3.33',
      '3.33',
    ])
    expect(splitPreview('10', 'ratio', [member({ id: 'z', share_percent: '0' })], 2)).toBeNull()
    expect(splitPreview('abc', 'even', [a], 2)).toBeNull()
    expect(splitPreview('10', 'even', [], 2)).toBeNull()
  })

  it('退款解除关联时发出显式 null', () => {
    const linked: LedgerEntry = { ...baseEntry, kind: 'refund', refunded_entry_id: 'e0' }
    const draft = ledgerDraftFrom(linked)
    draft.refunded_entry_id = ''
    const patch = changedLedgerFields(validateLedgerDraft(draft, cny), linked)
    expect(patch).toEqual({ refunded_entry_id: null })
  })
})
