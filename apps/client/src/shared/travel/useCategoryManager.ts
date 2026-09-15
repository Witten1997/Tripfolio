import { computed, reactive, ref, shallowRef } from 'vue'

import { ApiError } from '@/shared/api/auth'
import {
  createCategory,
  deleteCategory,
  getCategory,
  listCategories,
  updateCategory,
  type CategoryCreate,
  type CategoryPatch,
  type ExpenseCategory,
} from '@/shared/api/categories'
import { actionError, createWriteIntent, fieldErrors, writeWarnings } from '@/shared/api/writes'
import { randomId } from '@/shared/randomId'
import { useMetadataStore } from '@/shared/stores/metadata'

export const categoryIcons: Record<string, { label: string; symbol: string }> = {
  transport: { label: '交通', symbol: '🚆' },
  lodging: { label: '住宿', symbol: '🏨' },
  food: { label: '美食', symbol: '🍜' },
  attraction: { label: '景点', symbol: '🏞️' },
  shopping: { label: '购物', symbol: '🛍️' },
  entertainment: { label: '娱乐', symbol: '🎭' },
  ticket: { label: '票券', symbol: '🎫' },
  gift: { label: '礼物', symbol: '🎁' },
  medical: { label: '医疗', symbol: '💊' },
  other: { label: '其他', symbol: '📌' },
}

export function categoryIconLabel(icon: string | null) {
  return icon ? (categoryIcons[icon]?.label ?? icon) : '无图标'
}

export function useCategoryManager() {
  const metadata = useMetadataStore()
  const opened = ref(false)
  const loading = ref(false)
  const saving = ref(false)
  const uncertainCreate = ref(false)
  const deleting = ref<string | null>(null)
  const items = shallowRef<ExpenseCategory[]>([])
  const loadError = ref<string | null>(null)
  const error = ref<string | null>(null)
  const feedback = ref<string | null>(null)
  const errors = ref<Record<string, string>>({})
  const active = ref(false)
  const baseline = shallowRef<ExpenseCategory | null>(null)
  const latest = shallowRef<ExpenseCategory | null>(null)
  const conflict = ref(false)
  const loadingLatest = ref(false)
  const draft = reactive({
    name: '',
    icon: '' as string | null,
    sort_order: 0 as number | undefined,
  })
  const initial = ref('')
  const dirty = computed(() => active.value && JSON.stringify(draft) !== initial.value)
  const intent = createWriteIntent()
  const deleteIntents = new Map<string, ReturnType<typeof createWriteIntent>>()
  let createId = ''
  // 创建可能已提交：确认结果前保留原正文与操作号，不从可变草稿重新生成请求。
  let pendingCreate: { body: CategoryCreate; operationId: string } | null = null
  let generation = 0
  let editorGeneration = 0

  async function refreshList() {
    const request = ++generation
    loading.value = true
    loadError.value = null
    try {
      const result = await listCategories()
      if (request === generation) items.value = result
    } catch (cause) {
      if (request === generation) loadError.value = actionError(cause, '无法加载分类，请重试')
    } finally {
      if (request === generation) loading.value = false
    }
  }

  async function load() {
    if (saving.value || deleting.value || uncertainCreate.value) return
    await refreshList()
  }

  function start(category?: ExpenseCategory) {
    if (saving.value || deleting.value || uncertainCreate.value) return
    editorGeneration++
    loadingLatest.value = false
    active.value = true
    baseline.value = category ?? null
    latest.value = null
    conflict.value = false
    error.value = null
    errors.value = {}
    Object.assign(draft, {
      name: category?.name ?? '',
      icon: category?.icon ?? '',
      sort_order:
        category?.sort_order ??
        Math.min(2147483647, Math.max(-1, ...items.value.map((item) => item.sort_order)) + 1),
    })
    initial.value = JSON.stringify(draft)
    createId = randomId()
    intent.reset()
    pendingCreate = null
  }

  async function open() {
    if (saving.value || deleting.value || uncertainCreate.value) return
    editorGeneration++
    loadingLatest.value = false
    opened.value = true
    active.value = false
    feedback.value = error.value = null
    if (metadata.status === 'idle' || metadata.status === 'error') void metadata.load()
    await load()
  }

  function close(discardUncertain = false) {
    if (saving.value || deleting.value || (uncertainCreate.value && !discardUncertain)) return
    generation++
    editorGeneration++
    opened.value = false
    active.value = false
    pendingCreate = null
    uncertainCreate.value = false
  }

  async function loadLatest() {
    if (!baseline.value || loadingLatest.value) return
    const request = editorGeneration
    const id = baseline.value.id
    loadingLatest.value = true
    try {
      const category = await getCategory(id)
      if (request === editorGeneration && opened.value) latest.value = category
    } catch (cause) {
      if (request === editorGeneration) error.value = actionError(cause, '无法加载最新分类，请重试')
    } finally {
      if (request === editorGeneration) loadingLatest.value = false
    }
  }

  function adoptLatest() {
    if (latest.value) start(latest.value)
  }

  async function save(againstLatest = false) {
    if (
      !opened.value ||
      saving.value ||
      deleting.value ||
      !active.value ||
      (conflict.value && !againstLatest)
    )
      return false
    if (againstLatest && !latest.value) return false
    errors.value = {}
    error.value = null
    let patch: CategoryPatch = {}
    let createRequest = pendingCreate
    if (!createRequest) {
      const name = draft.name.trim()
      const icon = draft.icon || null
      if (!name || name.length > 40) errors.value.name = '请输入 1–40 个字符的分类名称'
      if (
        draft.sort_order === undefined ||
        !Number.isSafeInteger(draft.sort_order) ||
        draft.sort_order < 0 ||
        draft.sort_order > 2147483647
      )
        errors.value.sort_order = '请输入 0–2147483647 的整数排序值'
      if (metadata.status !== 'ready' || !metadata.metadata)
        errors.value.icon = '请先加载分类图标信息'
      else if (icon && !metadata.metadata.expense_category_icons.includes(icon))
        errors.value.icon = '请选择支持的图标，或清空图标'
      if (Object.keys(errors.value).length) return false
      const values = { name, icon, sort_order: draft.sort_order as number }
      if (baseline.value) {
        patch = Object.fromEntries(
          Object.entries(values).filter(
            ([key, value]) => value !== baseline.value?.[key as keyof typeof values],
          ),
        )
        if (!Object.keys(patch).length) {
          error.value = '没有需要保存的修改'
          return false
        }
      } else {
        createRequest = {
          body: { id: createId, ...values },
          operationId: intent.key({ id: createId, values }),
        }
      }
    }
    saving.value = true
    try {
      const base = againstLatest ? latest.value : baseline.value
      const outcome = base
        ? await updateCategory(
            base.id,
            base.version,
            patch,
            intent.key({ id: base.id, version: base.version, patch }),
          )
        : await createCategory(createRequest!.body, createRequest!.operationId)
      feedback.value = [
        '分类已保存，所有旅行都会使用更新后的分类。',
        ...writeWarnings(outcome.result),
      ].join(' ')
      intent.reset()
      pendingCreate = null
      uncertainCreate.value = false
      active.value = false
      await refreshList()
      return true
    } catch (cause) {
      error.value = actionError(cause, '网络连接中断，结果尚未确认。保留输入重试可避免重复提交。')
      errors.value = fieldErrors(cause)
      if (createRequest) {
        uncertainCreate.value =
          !(cause instanceof ApiError) || (cause.problem?.status ?? 500) >= 500
        pendingCreate = uncertainCreate.value ? createRequest : null
        if (uncertainCreate.value) {
          error.value = '创建结果尚未确认，分类可能已经保存。请原样重试，确认后再编辑内容。'
        }
      }
      if (cause instanceof ApiError && cause.code === 'VERSION_CONFLICT') {
        conflict.value = true
        latest.value = null
        await loadLatest()
      }
      return false
    } finally {
      saving.value = false
    }
  }

  async function remove(category: ExpenseCategory, confirmed: boolean) {
    if (!confirmed || saving.value || deleting.value || uncertainCreate.value) return false
    deleting.value = category.id
    error.value = null
    const operation = deleteIntents.get(category.id) ?? createWriteIntent()
    deleteIntents.set(category.id, operation)
    try {
      const outcome = await deleteCategory(
        category.id,
        category.version,
        operation.key({ id: category.id, version: category.version }),
      )
      operation.reset()
      feedback.value = ['分类已删除。', ...writeWarnings(outcome.result)].join(' ')
      if (baseline.value?.id === category.id) active.value = false
      await refreshList()
      return true
    } catch (cause) {
      error.value = actionError(cause, '网络连接中断，结果尚未确认。可重试删除或刷新分类列表。')
      if (
        cause instanceof ApiError &&
        ['VERSION_CONFLICT', 'CATEGORY_IN_USE'].includes(cause.code ?? '')
      )
        await refreshList()
      return false
    } finally {
      deleting.value = null
    }
  }

  return {
    opened,
    loading,
    saving,
    uncertainCreate,
    deleting,
    items,
    loadError,
    error,
    feedback,
    errors,
    active,
    baseline,
    latest,
    conflict,
    loadingLatest,
    draft,
    dirty,
    load,
    start,
    open,
    close,
    loadLatest,
    adoptLatest,
    save,
    remove,
  }
}
