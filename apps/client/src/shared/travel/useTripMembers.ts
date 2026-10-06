import { computed, reactive, ref, shallowRef } from 'vue'

import type { CollectionBaseline } from '@/shared/api/collectionGuards'
import { ApiError } from '@/shared/api/auth'
import {
  listTripMembersWithBaseline,
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
  type Snapshot = Awaited<ReturnType<typeof listTripMembersWithBaseline>>
  type Pending = { members: TripMemberInput[]; baseline: CollectionBaseline; operationId: string }
  const opened = ref(false)
  const loading = ref(false)
  const saving = ref(false)
  const checking = ref(false)
  const uncertain = ref(false)
  const needsReview = ref(false)
  const loadError = ref<string | null>(null)
  const error = ref<string | null>(null)
  const feedback = ref<string | null>(null)
  const latestError = ref<string | null>(null)
  const errors = ref<Record<string, string>>({})
  const items = shallowRef<TripMember[]>([])
  const baseline = shallowRef<CollectionBaseline | null>(null)
  const latest = shallowRef<Snapshot | null>(null)
  const rows = reactive<MemberRow[]>([])
  const initial = ref('[]')
  const intent = createWriteIntent()
  let pending: Pending | null = null
  let generation = 0

  const gap = computed(() => percentGap(rows))
  const dirty = computed(() => JSON.stringify(rows) !== initial.value)
  const editingLocked = computed(
    () => saving.value || loading.value || uncertain.value || !baseline.value,
  )
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
      opened.value &&
      !saving.value &&
      !loading.value &&
      !checking.value &&
      (uncertain.value ||
        (!!baseline.value &&
          !needsReview.value &&
          rows.length >= 1 &&
          rows.length <= MAX_TRIP_MEMBERS &&
          gap.value === 0 &&
          Object.keys(invalidRows.value).length === 0 &&
          dirty.value)),
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

  function adopt(snapshot: Snapshot) {
    items.value = snapshot.members
    baseline.value = snapshot.baseline
    reset(snapshot.members)
    latest.value = null
    needsReview.value = false
    intent.reset()
  }

  async function read() {
    const request = ++generation
    loading.value = true
    loadError.value = null
    try {
      const result = await listTripMembersWithBaseline(tripId)
      if (request === generation && opened.value) adopt(result)
    } catch (cause) {
      if (request === generation && opened.value)
        loadError.value =
          cause instanceof Error && !(cause instanceof ApiError)
            ? cause.message
            : actionError(cause, '无法加载成员，请重试')
    } finally {
      if (request === generation) loading.value = false
    }
  }

  async function loadLatest() {
    if (!opened.value || saving.value || uncertain.value || loading.value || checking.value) return
    const request = ++generation
    checking.value = true
    latest.value = null
    latestError.value = null
    try {
      const result = await listTripMembersWithBaseline(tripId)
      if (request === generation && opened.value) latest.value = result
    } catch (cause) {
      if (request === generation && opened.value)
        latestError.value =
          cause instanceof Error && !(cause instanceof ApiError)
            ? cause.message
            : actionError(cause, '无法读取最新成员，当前输入已保留')
    } finally {
      if (request === generation) checking.value = false
    }
  }

  function adoptLatest() {
    if (!latest.value || saving.value || uncertain.value || checking.value || !opened.value) return
    adopt(latest.value)
    error.value = loadError.value = latestError.value = feedback.value = null
    errors.value = {}
  }

  async function load() {
    if (!opened.value || saving.value || uncertain.value || loading.value || checking.value) return
    if (dirty.value || needsReview.value) return loadLatest()
    await read()
  }

  async function open() {
    if (saving.value || opened.value) return
    opened.value = true
    if (uncertain.value) return
    generation++
    baseline.value = latest.value = null
    rows.splice(0)
    initial.value = '[]'
    error.value = feedback.value = latestError.value = null
    errors.value = {}
    needsReview.value = false
    await read()
  }

  // Unconfirmed submissions survive hiding and reopening the dialog.
  function close() {
    if (saving.value) return
    generation++
    opened.value = false
    loading.value = checking.value = false
    latest.value = null
  }

  function add() {
    if (editingLocked.value || rows.length >= MAX_TRIP_MEMBERS) return
    rows.push({ id: randomId(), name: '', share_percent: '0', is_self: false, isNew: true })
  }
  function remove(index: number) {
    if (editingLocked.value) return
    const row = rows[index]
    if (!row || row.is_self) return
    rows.splice(index, 1)
  }
  function move(index: number, delta: number) {
    if (editingLocked.value) return
    const target = index + delta
    if (index < 0 || target < 0 || index >= rows.length || target >= rows.length) return
    const [row] = rows.splice(index, 1)
    rows.splice(target, 0, row!)
  }
  function equalize() {
    if (editingLocked.value) return
    distributeEvenly(rows.length).forEach((value, i) => {
      rows[i]!.share_percent = value
    })
  }

  async function save() {
    if (!canSave.value) return false
    const request =
      pending ??
      (() => {
        const members = rows.map((r) =>
          Object.freeze({
            id: r.id,
            name: r.name.trim(),
            share_percent: r.share_percent.trim(),
          }),
        )
        Object.freeze(members)
        const captured = baseline.value!
        return Object.freeze({
          members,
          baseline: captured,
          operationId: intent.key(captured.intent({ tripId, members })),
        })
      })()
    saving.value = true
    error.value = null
    errors.value = {}
    try {
      const result = await saveTripMembers(
        tripId,
        request.members,
        request.operationId,
        request.baseline,
      )
      pending = null
      uncertain.value = false
      intent.reset()
      needsReview.value = false
      latest.value = null
      // A confirmed write is not retried if its follow-up GET fails.
      const self = new Set(items.value.filter((m) => m.is_self).map((m) => m.id))
      rows.splice(
        0,
        rows.length,
        ...request.members.map((m) => ({ ...m, is_self: self.has(m.id), isNew: false })),
      )
      initial.value = JSON.stringify(rows)
      baseline.value = null
      feedback.value = ['成员已保存。', ...writeWarnings(result)].join(' ')
      await read()
      return true
    } catch (cause) {
      const status = cause instanceof ApiError ? cause.problem?.status : undefined
      uncertain.value ||= !status || status >= 500 || status === 408 || status === 429
      pending = uncertain.value ? request : null
      error.value = uncertain.value
        ? '保存结果尚未确认。请原样重试，确认前不能修改成员或重新加载。'
        : actionError(cause, '保存失败，当前输入已保留。')
      errors.value = fieldErrors(cause)
      if (
        cause instanceof ApiError &&
        ['COLLECTION_CONFLICT', 'COLLECTION_BASE_REQUIRED'].includes(cause.code ?? '')
      )
        needsReview.value = true
      return false
    } finally {
      saving.value = false
    }
  }

  return {
    opened,
    loading,
    saving,
    checking,
    uncertain,
    needsReview,
    editingLocked,
    loadError,
    error,
    feedback,
    latestError,
    errors,
    items,
    baseline,
    latest,
    rows,
    gap,
    dirty,
    invalidRows,
    canSave,
    open,
    close,
    load,
    loadLatest,
    adoptLatest,
    add,
    remove,
    move,
    equalize,
    save,
  }
}
