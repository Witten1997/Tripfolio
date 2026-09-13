import { onScopeDispose, ref, shallowRef, watch } from 'vue'

import { actionError } from '@/shared/api/writes'

interface Page<T> {
  items: T[]
  next_cursor: string | null
}

/** 每次重载使旧请求失效，避免筛选、刷新与下一页并发时混入旧列表。 */
export function useCursorPage<T extends { id: string }, Q extends object>(
  query: () => Q,
  fetchPage: (query: Q & { cursor?: string }) => Promise<Page<T>>,
) {
  const items = shallowRef<T[]>([])
  const cursor = ref<string | null>(null)
  const loading = ref(false)
  const loadingMore = ref(false)
  const error = ref<string | null>(null)
  const moreError = ref<string | null>(null)
  let generation = 0

  async function reload() {
    const request = ++generation
    const params = { ...query() }
    loading.value = true
    loadingMore.value = false
    items.value = []
    cursor.value = null
    error.value = moreError.value = null
    try {
      const page = await fetchPage(params)
      if (request !== generation) return
      items.value = page.items
      cursor.value = page.next_cursor
    } catch (cause) {
      if (request === generation) error.value = actionError(cause, '无法加载列表，请检查网络后重试')
    } finally {
      if (request === generation) loading.value = false
    }
  }

  async function loadMore() {
    if (loading.value || loadingMore.value || !cursor.value) return
    const request = generation
    const params = { ...query(), cursor: cursor.value }
    loadingMore.value = true
    moreError.value = null
    try {
      const page = await fetchPage(params)
      if (request !== generation) return
      const existing = new Set(items.value.map((item) => item.id))
      items.value = [...items.value, ...page.items.filter((item) => !existing.has(item.id))]
      cursor.value = page.next_cursor
    } catch (cause) {
      if (request === generation) moreError.value = actionError(cause, '下一页加载失败，请重试')
    } finally {
      if (request === generation) loadingMore.value = false
    }
  }

  watch(query, reload, { deep: true, immediate: true, flush: 'sync' })
  onScopeDispose(() => {
    generation++
  })

  return { items, cursor, loading, loadingMore, error, moreError, reload, loadMore }
}
