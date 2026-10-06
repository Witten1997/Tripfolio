import type { components } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import { versionHeaders, writeOutcome } from '@/shared/api/writes'
import type { CollectionBaseline } from '@/shared/api/collectionGuards'
import { useSessionStore } from '@/shared/stores/session'

export type ExpenseCategory = components['schemas']['ExpenseCategory']
export type CategoryCreate = components['schemas']['ExpenseCategoryCreate']
export type CategoryPatch = components['schemas']['ExpenseCategoryPatch']

function isCategory(value: Record<string, unknown>) {
  return (
    typeof value.id === 'string' &&
    typeof value.version === 'string' &&
    typeof value.name === 'string' &&
    typeof value.sort_order === 'number'
  )
}

export function sortCategories(categories: ExpenseCategory[]) {
  return [...categories].sort(
    (left, right) =>
      left.sort_order - right.sort_order || (left.id < right.id ? -1 : left.id > right.id ? 1 : 0),
  )
}

export async function listCategories(): Promise<ExpenseCategory[]> {
  const { data, error } = await api.GET('/expense-categories')
  if (error || !data) throw new ApiError(error)
  return sortCategories(data.data)
}

export async function getCategory(id: string): Promise<ExpenseCategory> {
  const { data, error } = await api.GET('/expense-categories/{category_id}', {
    params: { path: { category_id: id } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function createCategory(input: CategoryCreate, operationId: string) {
  const { data, error } = await api.POST('/expense-categories', {
    params: { header: { 'Idempotency-Key': operationId } },
    body: input,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<ExpenseCategory>(data.data, isCategory)
}

export async function updateCategory(
  id: string,
  version: string,
  patch: CategoryPatch,
  operationId: string,
  baseline?: CollectionBaseline,
) {
  if (
    baseline &&
    (!Object.hasOwn(patch, 'sort_order') ||
      baseline.guards.length !== 1 ||
      baseline.guards[0]?.kind !== 'categories' ||
      baseline.guards[0]?.scope_id !== useSessionStore().account?.id)
  ) {
    throw new Error('分类排序基线不属于当前账号。')
  }
  const { data, error } = await api.PATCH('/expense-categories/{category_id}', {
    params: { path: { category_id: id }, header: versionHeaders(operationId, version) },
    headers: baseline?.headers,
    body: patch,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<ExpenseCategory>(data.data, isCategory)
}

export async function deleteCategory(id: string, version: string, operationId: string) {
  const { data, error } = await api.DELETE('/expense-categories/{category_id}', {
    params: { path: { category_id: id }, header: versionHeaders(operationId, version) },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<ExpenseCategory>(data.data, isCategory)
}
