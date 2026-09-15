import { describe, expect, it } from 'vitest'

import type { ItineraryItem } from '@/shared/api/itinerary'
import type { PackingItem } from '@/shared/api/packing'
import type { Todo } from '@/shared/api/todos'
import {
  applyItineraryPlace,
  changedItineraryFields,
  clearItineraryPlace,
  emptyItineraryDraft,
  itineraryDraftFrom,
  itineraryExpensePreset,
  validateItineraryDraft,
} from '@/shared/travel/itineraryDraft'
import {
  changedPackingFields,
  packingDraftFrom,
  validatePackingDraft,
} from '@/shared/travel/packingDraft'
import { changedTodoFields, todoDraftFrom, validateTodoDraft } from '@/shared/travel/todoDraft'

const baseItem: ItineraryItem = {
  id: 'i1',
  trip_id: 't',
  title: '浅草寺',
  kind: 'attraction',
  scheduled_on: '2026-10-02',
  sort_order: 0,
  planned_start_local: '2026-10-02T09:00:00',
  planned_end_local: null,
  planned_duration_minutes: 90,
  place_name: '浅草',
  address: '',
  latitude: null,
  longitude: null,
  estimated_amount: '1500',
  currency_code: 'JPY',
  notes: '',
  status: 'pending',
  actual_start_local: null,
  actual_end_local: null,
  actual_notes: '',
  version: '1',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
  deleted_at: null,
}

describe('itineraryDraft', () => {
  it('新建须选点，换选时名称与位置一并更新，清除不遗留旧地址', () => {
    const draft = emptyItineraryDraft('2026-10-02')
    draft.title = '旧标题'
    const money = { currency: 'CNY', minorUnits: 2 }
    expect(() => validateItineraryDraft(draft, money, true)).toThrow('选点')
    for (const name of ['故宫', '景山公园'])
      applyItineraryPlace(draft, {
        name,
        address: '北京市东城区',
        latitude: 39.9,
        longitude: 116.4,
        poi_id: null,
        adcode: null,
        provider: 'amap',
      })
    expect(validateItineraryDraft(draft, money, true)).toMatchObject({
      title: '景山公园',
      place_name: '景山公园',
      address: '北京市东城区',
      latitude: 39.9,
      longitude: 116.4,
    })
    clearItineraryPlace(draft)
    expect(draft).toMatchObject({
      title: '',
      place_name: '',
      address: '',
      latitude: null,
      longitude: null,
    })
  })

  it('地点账单预填当前日期与地点，既有预计费用不变成账单金额', () => {
    const draft = itineraryDraftFrom(baseItem)
    expect(itineraryExpensePreset(draft)).toEqual({ notes: '浅草', occurred_on: '2026-10-02' })
    draft.scheduled_on = '2026-10-03'
    draft.place_name = '雷门'
    expect(itineraryExpensePreset(draft)).toEqual({ notes: '雷门', occurred_on: '2026-10-03' })
    expect(draft.estimated_amount).toBe('1500')
    draft.scheduled_on = ''
    expect(() => itineraryExpensePreset(draft)).toThrow('所属日期')
    clearItineraryPlace(draft)
    expect(() => itineraryExpensePreset(draft)).toThrow('地点')
  })

  it('编辑旧无定位记录不强制补选点，也不改写名称和历史费用', () => {
    const draft = itineraryDraftFrom(baseItem)
    draft.notes = '新备注'
    expect(
      changedItineraryFields(
        validateItineraryDraft(draft, { currency: 'JPY', minorUnits: 0 }),
        baseItem,
      ),
    ).toEqual({ notes: '新备注' })
  })
  it('草稿把当地时间拆成日期与时刻，时长模式与结束模式互斥', () => {
    const draft = itineraryDraftFrom(baseItem)
    expect(draft.planned_start).toBe('2026-10-02 09:00')
    expect(draft.planned_mode).toBe('duration')
    expect(draft.planned_duration_minutes).toBe('90')
    expect(draft.estimated_amount).toBe('1500')
  })

  it('校验输出契约字段：时长模式清空结束时间，金额按旅行币种规范化并附币种', () => {
    const draft = itineraryDraftFrom(baseItem)
    draft.planned_mode = 'end'
    draft.planned_end = '2026-10-02 11:30'
    draft.estimated_amount = '1500'
    const values = validateItineraryDraft(draft, { currency: 'JPY', minorUnits: 0 })
    expect(values.planned_end_local).toBe('2026-10-02T11:30:00')
    expect(values.planned_duration_minutes).toBeNull()
    expect(values.planned_start_local).toBe('2026-10-02T09:00:00')
    expect(values.estimated_amount).toBe('1500')
    expect(values.currency_code).toBe('JPY')
  })

  it('金额清空时不携带币种，日期与时间顺序在本地校验', () => {
    const draft = itineraryDraftFrom(baseItem)
    draft.estimated_amount = ''
    const values = validateItineraryDraft(draft, { currency: 'JPY', minorUnits: 0 })
    expect(values.estimated_amount).toBeNull()
    expect(values.currency_code).toBeUndefined()
    draft.planned_mode = 'end'
    draft.planned_end = '2026-10-02 08:00'
    expect(() => validateItineraryDraft(draft, { currency: 'JPY', minorUnits: 0 })).toThrow(
      '结束时间',
    )
    draft.planned_end = ''
    draft.title = ' '
    expect(() => validateItineraryDraft(draft, { currency: 'JPY', minorUnits: 0 })).toThrow('标题')
    draft.title = 'x'
    draft.scheduled_on = '2026-13-01'
    expect(() => validateItineraryDraft(draft, { currency: 'JPY', minorUnits: 0 })).toThrow('日期')
  })

  it('差异只含变化字段，创建字段 scheduled_on 不进 PATCH，清空金额时不发币种', () => {
    const draft = itineraryDraftFrom(baseItem)
    draft.title = '雷门'
    draft.estimated_amount = ''
    draft.scheduled_on = '2026-10-03'
    const patch = changedItineraryFields(
      validateItineraryDraft(draft, { currency: 'JPY', minorUnits: 0 }),
      baseItem,
    )
    expect(patch).toEqual({ title: '雷门', estimated_amount: null })
    draft.estimated_amount = '2000'
    const withAmount = changedItineraryFields(
      validateItineraryDraft(draft, { currency: 'JPY', minorUnits: 0 }),
      baseItem,
    )
    expect(withAmount).toEqual({ title: '雷门', estimated_amount: '2000', currency_code: 'JPY' })
  })
})

const basePacking: PackingItem = {
  id: 'p1',
  trip_id: 't',
  name: '护照',
  category: 'documents',
  quantity: 1,
  notes: '',
  status: 'pending',
  version: '1',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
  deleted_at: null,
}

describe('packingDraft', () => {
  it('数量以字符串编辑，校验为 1–9999 的整数', () => {
    const draft = packingDraftFrom(basePacking)
    draft.quantity = '3'
    expect(validatePackingDraft(draft).quantity).toBe(3)
    draft.quantity = '0'
    expect(() => validatePackingDraft(draft)).toThrow('数量')
    draft.quantity = '2.5'
    expect(() => validatePackingDraft(draft)).toThrow('数量')
    draft.quantity = '1'
    draft.name = ''
    expect(() => validatePackingDraft(draft)).toThrow('名称')
  })

  it('差异只含变化字段', () => {
    const draft = packingDraftFrom(basePacking)
    draft.status = 'ready'
    draft.name = ' 护照 '
    expect(changedPackingFields(validatePackingDraft(draft), basePacking)).toEqual({
      status: 'ready',
    })
  })

  it('旧 packed 编辑为已准备，无关修改不顺带迁移状态，取消勾选才写 pending', () => {
    const original = { ...basePacking, status: 'packed' as const }
    const draft = packingDraftFrom(original)
    expect(draft.status).toBe('ready')
    draft.notes = '放随身包'
    expect(changedPackingFields(validatePackingDraft(draft), original)).toEqual({
      notes: '放随身包',
    })
    draft.status = 'pending'
    expect(changedPackingFields(validatePackingDraft(draft), original)).toEqual({
      notes: '放随身包',
      status: 'pending',
    })
  })
})

const baseTodo: Todo = {
  id: 'd1',
  trip_id: 't',
  title: '换日元',
  due_on: '2026-09-20',
  notes: '',
  completed: false,
  completed_at: null,
  version: '1',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
  deleted_at: null,
}

describe('todoDraft', () => {
  it('清空截止日期输出显式 null 并进入 PATCH', () => {
    const draft = todoDraftFrom(baseTodo)
    draft.due_on = ''
    const values = validateTodoDraft(draft)
    expect(values.due_on).toBeNull()
    expect(changedTodoFields(values, baseTodo)).toEqual({ due_on: null })
  })

  it('非法日期与空标题被拦截', () => {
    const draft = todoDraftFrom(baseTodo)
    draft.due_on = '2026-02-30'
    expect(() => validateTodoDraft(draft)).toThrow('截止日期')
    draft.due_on = ''
    draft.title = '   '
    expect(() => validateTodoDraft(draft)).toThrow('标题')
  })
})
