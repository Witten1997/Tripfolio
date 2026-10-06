import { computed, nextTick, onScopeDispose, reactive, ref, shallowRef, watch } from 'vue'

import { ApiError } from '@/shared/api/auth'
import {
  createCategory,
  deleteCategory,
  getCategory,
  sortCategories,
  updateCategory,
  type CategoryCreate,
  type CategoryPatch,
  type ExpenseCategory,
} from '@/shared/api/categories'
import { actionError, createWriteIntent, fieldErrors, writeWarnings } from '@/shared/api/writes'
import { randomId } from '@/shared/randomId'
import { useMetadataStore } from '@/shared/stores/metadata'
import { useSessionStore } from '@/shared/stores/session'
import { loadCollectionBaselineHttp } from '@/shared/api/collectionBaselinesHttp'
import type { CompleteCollection } from '@/shared/api/collectionBaselines'
import type { CollectionBaseline } from '@/shared/api/collectionGuards'

type CategoryCollection = CompleteCollection<'categories'>
type SortRequest = {
  owner: string
  id: string
  version: string
  patch: CategoryPatch
  operationId: string
  baseline: CollectionBaseline
}
const uncertain = (cause: unknown) =>
  !(cause instanceof ApiError) || (cause.problem?.status ?? 500) >= 500

export { categoryIconLabel } from '@/shared/travel/categoryIconVisuals'

export function useCategoryManager() {
  const metadata = useMetadataStore()
  const session = useSessionStore()
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
  const collection = shallowRef<CategoryCollection | null>(null)
  const targetOrder = shallowRef<string[] | null>(null)
  const orderState = ref<'idle' | 'ready' | 'saving' | 'unknown' | 'read_failed' | 'conflict'>(
    'idle',
  )
  const savedOrderCount = ref(0)
  const totalOrderCount = ref(0)
  const latestOrder = shallowRef<CategoryCollection | null>(null)
  const loadingOrder = ref(false)
  const hasPendingOrder = computed(() => targetOrder.value !== null)
  const orderUnknown = computed(() => orderState.value === 'unknown')
  const pendingOrderCount = computed(() =>
    Math.max(0, totalOrderCount.value - savedOrderCount.value),
  )
  const canReorder = computed(() => opened.value && !!collection.value && !loading.value && !busy())
  const sameOrderMembers = computed(
    () => !!latestOrder.value && sameIDs(latestOrder.value, targetOrder.value ?? []),
  )
  let pendingOrder: SortRequest | null = null
  let expectedRevision: string | null = null
  let controller: AbortController | null = null
  let orderController: AbortController | null = null
  let orderGeneration = 0
  let lifecycle = 0
  let boundOwner = session.account?.id
  let createId = ''
  // 创建可能已提交：确认结果前保留原正文与操作号，不从可变草稿重新生成请求。
  let pendingCreate: { body: CategoryCreate; operationId: string } | null = null
  let generation = 0
  let editorGeneration = 0

  function sameIDs(value: CategoryCollection, ids: readonly string[]) {
    return (
      value.items.length === ids.length &&
      new Set(ids).size === ids.length &&
      value.items.every((item) => ids.includes(item.id))
    )
  }
  function install(value: CategoryCollection) {
    collection.value = value
    const byId = new Map(value.items.map((item) => [item.id, { ...item }]))
    items.value = targetOrder.value
      ? targetOrder.value.map((id) => byId.get(id)!).filter(Boolean)
      : sortCategories([...byId.values()])
  }
  function clearOrder() {
    orderGeneration++
    orderController?.abort()
    latestOrder.value = null
    loadingOrder.value = false
    targetOrder.value = null
    pendingOrder = null
    expectedRevision = null
    orderState.value = 'idle'
    savedOrderCount.value = totalOrderCount.value = 0
  }
  function cancelReads() {
    generation++
    editorGeneration++
    orderGeneration++
    controller?.abort()
    orderController?.abort()
    loading.value = loadingLatest.value = loadingOrder.value = false
    latest.value = null
    latestOrder.value = null
  }
  const alive = (stamp: number, owner: string | undefined) =>
    stamp === lifecycle &&
    owner === boundOwner &&
    (!session.account?.id || owner === session.account.id) &&
    opened.value
  watch(
    () => session.account?.id,
    (owner) => {
      // A 401 clears authentication, not the original write's outcome. Keep that
      // account's frozen request until re-authentication; never send it as another account.
      if (!owner) {
        cancelReads()
        return
      }
      if (owner === boundOwner) return
      boundOwner = owner
      lifecycle++
      cancelReads()
      clearOrder()
      collection.value = null
      items.value = []
      active.value = false
      baseline.value = null
      Object.assign(draft, { name: '', icon: '' })
      pendingCreate = null
      uncertainCreate.value = saving.value = reordering.value = false
      deleting.value = null
      savedOrderCount.value = totalOrderCount.value = 0
      intent.reset()
      deleteIntents.clear()
      error.value = feedback.value = null
      loadError.value = '账号已变化，请重新加载当前账号的分类。'
    },
    { flush: 'sync' },
  )
  onScopeDispose(() => {
    lifecycle++
    cancelReads()
  }, true)

  async function refreshList() {
    const request = ++generation
    const owner = session.account?.id
    controller?.abort()
    controller = new AbortController()
    loading.value = true
    loadError.value = null
    try {
      if (!owner) throw new Error('请先登录后再读取分类。')
      const result = await loadCollectionBaselineHttp(
        { kind: 'categories', scope_id: owner },
        { signal: controller.signal },
      )
      if (request === generation && owner === session.account?.id && opened.value) install(result)
    } catch (cause) {
      if (request === generation) {
        collection.value = null
        loadError.value = actionError(cause, '无法完整加载分类，请重试')
      }
    } finally {
      if (request === generation) loading.value = false
    }
  }

  const busy = () =>
    !session.account?.id ||
    saving.value ||
    !!deleting.value ||
    reordering.value ||
    uncertainCreate.value ||
    hasPendingOrder.value

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
    cancelReads()
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
      orderUnknown.value ||
      (hasPendingOrder.value && !discardUncertain) ||
      (uncertainCreate.value && !discardUncertain)
    )
      return
    lifecycle++
    cancelReads()
    clearOrder()
    opened.value = false
    active.value = false
    pendingCreate = null
    uncertainCreate.value = false
  }

  async function loadLatest() {
    if (!baseline.value || loadingLatest.value || hasPendingOrder.value || uncertainCreate.value)
      return
    const request = editorGeneration
    const id = baseline.value.id
    loadingLatest.value = true
    latest.value = null
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
      !session.account?.id ||
      saving.value ||
      deleting.value ||
      reordering.value ||
      hasPendingOrder.value ||
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
    const stamp = lifecycle
    const owner = session.account?.id
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
      if (!alive(stamp, owner)) return false
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
      if (!alive(stamp, owner)) return false
      error.value = actionError(cause, '网络连接中断，结果尚未确认。保留输入重试可避免重复提交。')
      errors.value = fieldErrors(cause)
      if (createRequest) {
        uncertainCreate.value = uncertainCreate.value || uncertain(cause)
        pendingCreate = uncertainCreate.value ? createRequest : null
        if (uncertainCreate.value) {
          error.value = '创建结果尚未确认，分类可能已经保存。请原样重试，确认后再编辑内容。'
        }
      }
      if (
        !uncertainCreate.value &&
        cause instanceof ApiError &&
        cause.code === 'VERSION_CONFLICT'
      ) {
        conflict.value = true
        latest.value = null
        await loadLatest()
      }
      return false
    } finally {
      if (stamp === lifecycle) saving.value = false
    }
  }

  async function remove(category: ExpenseCategory, confirmed: boolean) {
    if (!confirmed || busy()) return false
    const stamp = lifecycle
    const owner = session.account?.id
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
      if (!alive(stamp, owner)) return false
      operation.reset()
      feedback.value = ['分类已删除。', ...writeWarnings(outcome.result)].join(' ')
      if (baseline.value?.id === category.id) active.value = false
      await refreshList()
      return true
    } catch (cause) {
      if (!alive(stamp, owner)) return false
      error.value = actionError(cause, '网络连接中断，结果尚未确认。可重试删除或刷新分类列表。')
      if (
        cause instanceof ApiError &&
        ['VERSION_CONFLICT', 'CATEGORY_IN_USE'].includes(cause.code ?? '')
      )
        await refreshList()
      return false
    } finally {
      if (stamp === lifecycle) deleting.value = null
    }
  }

  // A successful receipt confirms this step only. Re-read before advancing and
  // accept that read only when it matches the receipt's original collection fact.
  async function confirmOrderStep(stamp: number, owner: string) {
    if (!expectedRevision) return false
    if (owner !== session.account?.id) {
      orderState.value = 'read_failed'
      error.value = '顺序已保存，请重新登录原账号后核对结果。'
      return false
    }
    orderController = new AbortController()
    try {
      const next = await loadCollectionBaselineHttp(
        { kind: 'categories', scope_id: owner },
        { signal: orderController.signal },
      )
      if (!alive(stamp, owner) || owner !== session.account?.id) return false
      if (next.revision !== expectedRevision || !sameIDs(next, targetOrder.value ?? [])) {
        orderState.value = 'conflict'
        error.value = '已保存的部分已确认，但分类列表又发生变化。请核对最新分类，待保存顺序已保留。'
        return false
      }
      install(next)
      expectedRevision = null
      return true
    } catch (cause) {
      if (alive(stamp, owner)) {
        orderState.value = 'read_failed'
        error.value = actionError(
          cause,
          '这一条顺序已保存，但核对列表失败。请重试读取，不会重复保存。',
        )
      }
      return false
    }
  }

  async function continueOrder() {
    if (
      !targetOrder.value ||
      !collection.value ||
      reordering.value ||
      saving.value ||
      deleting.value ||
      uncertainCreate.value ||
      loadingOrder.value
    )
      return false
    if (!['ready', 'unknown', 'read_failed'].includes(orderState.value)) return false
    const owner = collection.value.scope_id
    if (owner !== session.account?.id) return false
    const stamp = lifecycle
    cancelOrderReview()
    reordering.value = true
    error.value = null
    try {
      if (expectedRevision && !(await confirmOrderStep(stamp, owner))) return false
      while (alive(stamp, owner)) {
        if (!pendingOrder) {
          const current = collection.value!
          const changed = targetOrder
            .value!.map((id, index) => ({
              item: current.items.find((item) => item.id === id),
              index,
            }))
            .find(({ item, index }) => item?.sort_order !== index)
          if (!changed) {
            targetOrder.value = null
            install(current)
            orderState.value = 'idle'
            feedback.value = '分类顺序已保存。'
            return true
          }
          if (!changed.item) {
            orderState.value = 'conflict'
            return false
          }
          const patch = Object.freeze({ sort_order: changed.index })
          const request = { id: changed.item.id, version: changed.item.version, patch }
          pendingOrder = {
            ...request,
            owner,
            baseline: current.baseline,
            operationId: createWriteIntent().key(current.baseline.intent(request)),
          }
        }
        const request = pendingOrder
        const wasUnknown = orderState.value === 'unknown'
        orderState.value = 'saving'
        try {
          const outcome = await updateCategory(
            request.id,
            request.version,
            request.patch,
            request.operationId,
            request.baseline,
          )
          if (!alive(stamp, owner)) return false
          pendingOrder = null
          savedOrderCount.value++
          const facts = outcome.result.scope_revisions
          if (
            !facts ||
            facts.length !== 1 ||
            facts[0]?.kind !== 'categories' ||
            facts[0]?.scope_id !== owner ||
            !/^sha256:[0-9a-f]{64}$/.test(facts[0].revision)
          ) {
            orderState.value = 'conflict'
            error.value = '这一条顺序已保存，但收据缺少原分类基线。请核对最新分类后再继续。'
            return false
          }
          expectedRevision = facts[0].revision
        } catch (cause) {
          if (!alive(stamp, owner)) return false
          orderState.value = wasUnknown || uncertain(cause) ? 'unknown' : 'conflict'
          error.value =
            orderState.value === 'unknown'
              ? '排序结果尚未确认。请原样重试，确认前不能继续排序或离开。'
              : actionError(cause, '顺序未能继续保存，请核对最新分类。目标顺序和已保存进度已保留。')
          return false
        }
        if (!(await confirmOrderStep(stamp, owner))) return false
      }
      return false
    } finally {
      if (stamp === lifecycle) reordering.value = false
    }
  }

  async function reorder(orderedIds: string[]) {
    if (!canReorder.value || !collection.value || !sameIDs(collection.value, orderedIds))
      return false
    targetOrder.value = [...orderedIds]
    totalOrderCount.value = orderedIds.filter(
      (id, index) => collection.value!.items.find((item) => item.id === id)?.sort_order !== index,
    ).length
    savedOrderCount.value = 0
    install(collection.value)
    orderState.value = 'ready'
    feedback.value = null
    return continueOrder()
  }

  async function reviewOrder() {
    if (!hasPendingOrder.value || reordering.value || orderUnknown.value || loadingOrder.value)
      return
    const owner = session.account?.id
    if (!owner) return
    const request = ++orderGeneration
    orderController?.abort()
    orderController = new AbortController()
    latestOrder.value = null
    loadingOrder.value = true
    try {
      const value = await loadCollectionBaselineHttp(
        { kind: 'categories', scope_id: owner },
        { signal: orderController.signal },
      )
      if (request === orderGeneration && opened.value && owner === session.account?.id)
        latestOrder.value = value
    } catch (cause) {
      if (request === orderGeneration)
        error.value = actionError(cause, '无法核对最新分类，请重试。')
    } finally {
      if (request === orderGeneration) loadingOrder.value = false
    }
  }

  function cancelOrderReview() {
    orderGeneration++
    orderController?.abort()
    latestOrder.value = null
    loadingOrder.value = false
  }

  // The UI confirms which intent to keep. Adoption itself never submits a write.
  function adoptOrder(keepTarget: boolean) {
    const next = latestOrder.value
    if (
      !next ||
      reordering.value ||
      orderUnknown.value ||
      loadingOrder.value ||
      next.scope_id !== session.account?.id ||
      (keepTarget && !sameOrderMembers.value)
    )
      return false
    const target = keepTarget ? targetOrder.value : null
    clearOrder()
    targetOrder.value = target
    install(next)
    savedOrderCount.value = 0
    totalOrderCount.value =
      target?.filter((id, index) => next.items.find((item) => item.id === id)?.sort_order !== index)
        .length ?? 0
    orderState.value = target ? 'ready' : 'idle'
    error.value = null
    return true
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
    collection,
    targetOrder,
    orderState,
    hasPendingOrder,
    orderUnknown,
    savedOrderCount,
    pendingOrderCount,
    canReorder,
    latestOrder,
    loadingOrder,
    sameOrderMembers,
    continueOrder,
    reviewOrder,
    cancelOrderReview,
    adoptOrder,
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
 * 失败时保留管理器中的目标顺序，不用任意最新列表覆盖拖动草稿。
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
    if (!manager.canReorder.value) return
    dragging.value = true
  }

  /** 鼠标拖动结束后浏览器仍会补发 click（触屏由 Sortable 吞掉），下一轮事件循环再解除拦截。 */
  async function onEnd() {
    setTimeout(() => (dragging.value = false))
    await commit()
  }

  /** 键盘移动会让 Vue 搬动 DOM 节点，浏览器随之丢焦点；节点仍是同一个，移动后重新聚焦即可连续操作。 */
  async function move(index: number, delta: number) {
    if (!manager.canReorder.value) return
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
