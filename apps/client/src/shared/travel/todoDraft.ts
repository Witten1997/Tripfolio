import type { Todo, TodoCreate, TodoPatch } from '@/shared/api/todos'
import { validDate } from '@/shared/travel/itineraryDraft'
import { DraftError } from '@/shared/travel/tripDraft'

export interface TodoDraft {
  title: string
  due_on: string
  notes: string
  completed: boolean
}

export type TodoValues = Omit<TodoCreate, 'id'> & {
  due_on: string | null
  notes: string
  completed: boolean
}

export const todoFieldLabels: Record<string, string> = {
  title: '标题',
  due_on: '截止日期',
  notes: '备注',
  completed: '完成状态',
}

export function emptyTodoDraft(): TodoDraft {
  return { title: '', due_on: '', notes: '', completed: false }
}

export function todoDraftFrom(todo: Todo): TodoDraft {
  return {
    title: todo.title,
    due_on: todo.due_on ?? '',
    notes: todo.notes,
    completed: todo.completed,
  }
}

export function validateTodoDraft(draft: TodoDraft): TodoValues {
  const errors: Record<string, string> = {}
  const title = draft.title.trim()
  if (!title || title.length > 200) errors.title = '请输入 1–200 个字符的标题'
  const due = draft.due_on.trim()
  if (due && !validDate(due)) errors.due_on = '请选择有效的截止日期'
  if (draft.notes.length > 4000) errors.notes = '备注最多 4000 个字符'
  if (Object.keys(errors).length) throw new DraftError(errors)
  return { title, due_on: due || null, notes: draft.notes, completed: draft.completed }
}

export function changedTodoFields(values: TodoValues, baseline: Todo): TodoPatch {
  const patch: Record<string, unknown> = {}
  for (const key of ['title', 'due_on', 'notes', 'completed'] as const) {
    if (values[key] !== baseline[key]) patch[key] = values[key]
  }
  return patch as TodoPatch
}
