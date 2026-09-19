import { computed, nextTick, reactive, ref, shallowRef, watch } from 'vue'

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

export { categoryIconLabel } from '@/shared/travel/categoryIconVisuals'

export function useCategoryManager() {
  const metadata = useMetadataStore()
  const opened = ref(false)
  const loading = ref(false)
  const saving = ref(false)
  const uncertainCreate = ref(false)
  const deleting = ref<string | null>(null)
  const reordering = ref(false)
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
  })
  const initial = ref('')
  const dirty = computed(() => active.value && JSON.stringify(draft) !== initial.value)
  const intent = createWriteIntent()
  const deleteIntents = new Map<string, ReturnType<typeof createWriteIntent>>()
  const reorderIntents = new Map<string, ReturnType<typeof createWriteIntent>>()
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

  const busy = () => saving.value || !!deleting.value || reordering.value || uncertainCreate.value

  async function load() {
    if (busy()) return
    await refreshList()
  }

  function start(category?: ExpenseCategory) {
    if (busy()) return
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
    })
    initial.value = JSON.stringify(draft)
    createId = randomId()
    intent.reset()
    pendingCreate = null
  }

  async function open() {
    if (busy()) return
    editorGeneration++
    loadingLatest.value = false
    opened.value = true
    active.value = false
    feedback.value = error.value = null
    if (metadata.status === 'idle' || metadata.status === 'error') void metadata.load()
    await load()
  }

  function close(discardUncertain = false) {
    if (
      saving.value ||
      deleting.value ||
      reordering.value ||
      (uncertainCreate.value && !discardUncertain)
    )
      return
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
      reordering.value ||
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
      if (metadata.status !== 'ready' || !metadata.metadata)
        errors.value.icon = '请先加载分类图标信息'
      else if (icon && !metadata.metadata.expense_category_icons.includes(icon))
        errors.value.icon = '请选择支持的图标，或清空图标'
      if (Object.keys(errors.value).length) return false
      const values = { name, icon }
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
    if (!confirmed || busy()) return false
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

  /**
   * 按拖动后的顺序重排：排序值取列表位置，只对位置变化的分类逐个 PATCH。
   * 任一请求失败即停止并刷新列表，已提交的部分保留在服务端。
   */
  async function reorder(orderedIds: string[]) {
    if (!opened.value || busy()) return false
    const byId = new Map(items.value.map((item) => [item.id, item]))
    const ordered = orderedIds.map((id) => byId.get(id)).filter((item) => !!item)
    if (ordered.length !== items.value.length || new Set(orderedIds).size !== orderedIds.length)
      return false
    const changes = ordered
      .map((item, index) => ({ item, sortOrder: index }))
      .filter(({ item, sortOrder }) => item.sort_order !== sortOrder)
    if (!changes.length) return true
    reordering.value = true
    error.value = null
    items.value = ordered.map((item, index) => ({ ...item, sort_order: index }))
    try {
      for (const { item, sortOrder } of changes) {
        const patch: CategoryPatch = { sort_order: sortOrder }
        const operation = reorderIntents.get(item.id) ?? createWriteIntent()
        reorderIntents.set(item.id, operation)
        await updateCategory(
          item.id,
          item.version,
          patch,
          operation.key({ id: item.id, version: item.version, patch }),
        )
        operation.reset()
      }
      feedback.value = '分类顺序已保存。'
      return true
    } catch (cause) {
      error.value = actionError(cause, '网络连接中断，排序结果尚未确认。刷新分类列表后可重新拖动。')
      return false
    } finally {
      await refreshList()
      // 排序只改 sort_order 与版本号，编辑中的草稿不受影响，直接换用刷新后的基线避免保存时版本冲突。
      const fresh = items.value.find((item) => item.id === baseline.value?.id)
      if (fresh) baseline.value = fresh
      reordering.value = false
    }
  }

  return {
    opened,
    loading,
    saving,
    uncertainCreate,
    deleting,
    reordering,
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
    reorder,
  }
}

/**
 * 拖动组件需要可变数组：维护分类列表的镜像，拖动或键盘移动后提交新顺序，
 * 无论成败都按服务端结果重建镜像。
 */
export function useCategoryCards(manager: ReturnType<typeof useCategoryManager>) {
  const cards = shallowRef<ExpenseCategory[]>([])
  const dragging = ref(false)
  watch(manager.items, (next) => (cards.value = [...next]), { immediate: true, flush: 'sync' })

  async function commit() {
    await manager.reorder(cards.value.map((item) => item.id))
    cards.value = [...manager.items.value]
  }

  function onStart() {
    dragging.value = true
  }

  /** 鼠标拖动结束后浏览器仍会补发 click（触屏由 Sortable 吞掉），下一轮事件循环再解除拦截。 */
  async function onEnd() {
    setTimeout(() => (dragging.value = false))
    await commit()
  }

  /** 键盘移动会让 Vue 搬动 DOM 节点，浏览器随之丢焦点；节点仍是同一个，移动后重新聚焦即可连续操作。 */
  async function move(index: number, delta: number) {
    const target = index + delta
    if (target < 0 || target >= cards.value.length) return
    const focused = typeof document === 'undefined' ? null : document.activeElement
    const next = [...cards.value]
    next.splice(target, 0, ...next.splice(index, 1))
    cards.value = next
    await nextTick()
    if (focused instanceof HTMLElement && focused.isConnected) focused.focus()
    await commit()
  }

  return { cards, dragging, onStart, onEnd, move }
}
