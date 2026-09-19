import { computed, reactive, ref, shallowRef } from 'vue'

import { ApiError } from '@/shared/api/auth'
import {
  listTripMembers,
  saveTripMembers,
  type TripMember,
  type TripMemberInput,
} from '@/shared/api/members'
import { actionError, createWriteIntent, fieldErrors, writeWarnings } from '@/shared/api/writes'
import { randomId } from '@/shared/randomId'

export interface MemberRow {
  id: string
  name: string
  share_percent: string
  is_self: boolean
  isNew: boolean
}

export const MAX_TRIP_MEMBERS = 50

/** 百分比字符串转为 0.01 单位整数；非法返回 null。 */
export function percentUnits(raw: string): number | null {
  const value = raw.trim()
  if (!/^(100(\.0{1,2})?|\d{1,2}(\.\d{1,2})?)$/.test(value)) return null
  return Math.round(Number(value) * 100)
}

/** 总和与 100 的差值（0.01 单位）；任一行非法时返回 null。 */
export function percentGap(rows: readonly { share_percent: string }[]): number | null {
  let total = 0
  for (const row of rows) {
    const units = percentUnits(row.share_percent)
    if (units === null) return null
    total += units
  }
  return 10000 - total
}

export function formatPercent(units: number): string {
  const sign = units < 0 ? '-' : ''
  const abs = Math.abs(units)
  const whole = Math.floor(abs / 100)
  const frac = abs % 100
  return frac === 0
    ? `${sign}${whole}`
    : `${sign}${whole}.${String(frac).padStart(2, '0').replace(/0$/, '')}`
}

/** 把剩余百分比均分给全部成员：整体保存前的一键校准。 */
export function distributeEvenly(count: number): string[] {
  if (count < 1) return []
  const base = Math.floor(10000 / count)
  let remainder = 10000 - base * count
  return Array.from({ length: count }, () => {
    const units = base + (remainder > 0 ? 1 : 0)
    if (remainder > 0) remainder--
    return formatPercent(units)
  })
}

export function useTripMembers(tripId: string) {
  const opened = ref(false)
  const loading = ref(false)
  const saving = ref(false)
  const loadError = ref<string | null>(null)
  const error = ref<string | null>(null)
  const feedback = ref<string | null>(null)
  const errors = ref<Record<string, string>>({})
  const items = shallowRef<TripMember[]>([])
  const rows = reactive<MemberRow[]>([])
  const initial = ref('')
  const intent = createWriteIntent()
  let generation = 0

  const gap = computed(() => percentGap(rows))
  const dirty = computed(() => JSON.stringify(rows) !== initial.value)
  const invalidRows = computed(() => {
    const names = new Set<string>()
    const out: Record<string, string> = {}
    rows.forEach((row, index) => {
      const name = row.name.trim()
      if (!name || [...name].length > 30) out[`${index}.name`] = '名称须为 1–30 个字符'
      else if (names.has(name.toLowerCase())) out[`${index}.name`] = '成员名称重复'
      names.add(name.toLowerCase())
      if (percentUnits(row.share_percent) === null)
        out[`${index}.share_percent`] = '百分比须为 0–100，最多 2 位小数'
    })
    return out
  })
  const canSave = computed(
    () =>
      rows.length >= 1 &&
      rows.length <= MAX_TRIP_MEMBERS &&
      gap.value === 0 &&
      Object.keys(invalidRows.value).length === 0 &&
      dirty.value,
  )

  function reset(from: TripMember[]) {
    rows.splice(
      0,
      rows.length,
      ...from.map((m) => ({
        id: m.id,
        name: m.name,
        share_percent: m.share_percent,
        is_self: m.is_self,
        isNew: false,
      })),
    )
    initial.value = JSON.stringify(rows)
  }

  async function load() {
    const request = ++generation
    loading.value = true
    loadError.value = null
    try {
      const result = await listTripMembers(tripId)
      if (request === generation) {
        items.value = result
        reset(result)
      }
    } catch (cause) {
      if (request === generation) loadError.value = actionError(cause, '无法加载成员，请重试')
    } finally {
      if (request === generation) loading.value = false
    }
  }

  async function open() {
    opened.value = true
    error.value = feedback.value = null
    errors.value = {}
    await load()
  }

  function close() {
    if (saving.value) return
    generation++
    opened.value = false
  }

  function add() {
    if (rows.length >= MAX_TRIP_MEMBERS) return
    rows.push({ id: randomId(), name: '', share_percent: '0', is_self: false, isNew: true })
  }

  function remove(index: number) {
    const row = rows[index]
    if (!row || row.is_self) return
    rows.splice(index, 1)
  }

  function move(index: number, delta: number) {
    const target = index + delta
    if (index < 0 || target < 0 || index >= rows.length || target >= rows.length) return
    const [row] = rows.splice(index, 1)
    rows.splice(target, 0, row!)
  }

  function equalize() {
    distributeEvenly(rows.length).forEach((value, i) => {
      rows[i]!.share_percent = value
    })
  }

  async function save() {
    if (!canSave.value || saving.value) return false
    saving.value = true
    error.value = null
    errors.value = {}
    const members: TripMemberInput[] = rows.map((r) => ({
      id: r.id,
      name: r.name.trim(),
      share_percent: r.share_percent.trim(),
    }))
    try {
      const result = await saveTripMembers(tripId, members, intent.key({ tripId, members }))
      intent.reset()
      feedback.value = ['成员已保存。', ...writeWarnings(result)].join(' ')
      await load()
      return true
    } catch (cause) {
      error.value = actionError(cause, '网络连接中断，结果尚未确认。保留输入重试可避免重复提交。')
      errors.value = fieldErrors(cause)
      if (cause instanceof ApiError && cause.code === 'MEMBER_IN_USE') {
        error.value = cause.message || '成员仍被账目引用，不能删除。'
      }
      return false
    } finally {
      saving.value = false
    }
  }

  return {
    opened,
    loading,
    saving,
    loadError,
    error,
    feedback,
    errors,
    items,
    rows,
    gap,
    dirty,
    invalidRows,
    canSave,
    open,
    close,
    load,
    add,
    remove,
    move,
    equalize,
    save,
  }
}
