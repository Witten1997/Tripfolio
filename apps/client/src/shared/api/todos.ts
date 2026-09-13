import type { components, operations } from '@tripfolio/contracts/openapi/v1'

import { ApiError } from '@/shared/api/auth'
import { api } from '@/shared/api/client'
import { versionHeaders, writeOutcome } from '@/shared/api/writes'

export type Todo = components['schemas']['Todo']
export type TodoListItem = components['schemas']['TodoListItem']
export type TodoCreate = components['schemas']['TodoCreate']
export type TodoPatch = components['schemas']['TodoPatch']
export type TodoState = components['schemas']['TodoState']
export type TodoQuery = NonNullable<operations['listTodos']['parameters']['query']>

export const todoStateLabels: Record<TodoState, string> = {
  all: '全部',
  pending: '未完成',
  completed: '已完成',
  overdue: '逾期',
}

function isTodo(value: Record<string, unknown>) {
  return (
    typeof value.id === 'string' &&
    typeof value.version === 'string' &&
    typeof value.title === 'string' &&
    typeof value.completed === 'boolean'
  )
}

export async function listTodos(tripId: string, query: TodoQuery = {}) {
  const { data, error } = await api.GET('/trips/{trip_id}/todos', {
    params: { path: { trip_id: tripId }, query },
  })
  if (error || !data) throw new ApiError(error)
  return data
}

export async function listAllTodos(tripId: string, state: TodoState = 'all') {
  const items: TodoListItem[] = []
  let cursor: string | undefined
  do {
    const page = await listTodos(tripId, { state, limit: 100, cursor })
    items.push(...page.items)
    cursor = page.next_cursor ?? undefined
  } while (cursor)
  return items
}

export async function getTodo(tripId: string, id: string): Promise<Todo> {
  const { data, error } = await api.GET('/trips/{trip_id}/todos/{todo_id}', {
    params: { path: { trip_id: tripId, todo_id: id } },
  })
  if (error || !data) throw new ApiError(error)
  return data.data
}

export async function createTodo(tripId: string, input: TodoCreate, operationId: string) {
  const { data, error } = await api.POST('/trips/{trip_id}/todos', {
    params: { path: { trip_id: tripId }, header: { 'Idempotency-Key': operationId } },
    body: input,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Todo>(data.data, isTodo)
}

export async function updateTodo(
  tripId: string,
  id: string,
  version: string,
  patch: TodoPatch,
  operationId: string,
) {
  const { data, error } = await api.PATCH('/trips/{trip_id}/todos/{todo_id}', {
    params: {
      path: { trip_id: tripId, todo_id: id },
      header: versionHeaders(operationId, version),
    },
    body: patch,
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Todo>(data.data, isTodo)
}

export async function deleteTodo(tripId: string, id: string, version: string, operationId: string) {
  const { data, error } = await api.DELETE('/trips/{trip_id}/todos/{todo_id}', {
    params: {
      path: { trip_id: tripId, todo_id: id },
      header: versionHeaders(operationId, version),
    },
  })
  if (error || !data) throw new ApiError(error)
  return writeOutcome<Todo>(data.data, isTodo)
}
