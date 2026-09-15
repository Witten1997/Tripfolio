import {
  normalizePackingStatus,
  type PackingCreate,
  type PackingItem,
  type PackingPatch,
  type PreparedPackingStatus,
} from '@/shared/api/packing'
import { DraftError } from '@/shared/travel/tripDraft'

export interface PackingDraft {
  name: string
  category: PackingItem['category']
  quantity: string
  notes: string
  status: PreparedPackingStatus
}

export type PackingValues = Omit<PackingCreate, 'id'> & {
  quantity: number
  notes: string
  status: PreparedPackingStatus
}

export const packingFieldLabels: Record<string, string> = {
  name: '物品名称',
  category: '分类',
  quantity: '数量',
  notes: '备注',
  status: '状态',
}

export function emptyPackingDraft(category: PackingItem['category'] = 'documents'): PackingDraft {
  return { name: '', category, quantity: '1', notes: '', status: 'pending' }
}

export function packingDraftFrom(item: PackingItem): PackingDraft {
  return {
    name: item.name,
    category: item.category,
    quantity: String(item.quantity),
    notes: item.notes,
    status: normalizePackingStatus(item.status),
  }
}

export function validatePackingDraft(draft: PackingDraft): PackingValues {
  const errors: Record<string, string> = {}
  const name = draft.name.trim()
  if (!name || name.length > 120) errors.name = '请输入 1–120 个字符的物品名称'
  const quantity = Number(draft.quantity.trim())
  if (!/^\d+$/.test(draft.quantity.trim()) || quantity < 1 || quantity > 9999) {
    errors.quantity = '数量须为 1–9999 的整数'
  }
  if (draft.notes.length > 2000) errors.notes = '备注最多 2000 个字符'
  if (Object.keys(errors).length) throw new DraftError(errors)
  return { name, category: draft.category, quantity, notes: draft.notes, status: draft.status }
}

export function changedPackingFields(values: PackingValues, baseline: PackingItem): PackingPatch {
  const patch: Record<string, unknown> = {}
  for (const key of ['name', 'category', 'quantity', 'notes'] as const) {
    if (values[key] !== baseline[key]) patch[key] = values[key]
  }
  // 修改名称等字段不能顺带把历史 packed 写成 ready。
  if (values.status !== normalizePackingStatus(baseline.status)) patch.status = values.status
  return patch as PackingPatch
}
