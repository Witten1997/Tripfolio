import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, reactive, type EffectScope } from 'vue'

import { useCursorPage } from '@/shared/travel/useCursorPage'
import { deferred } from '@/shared/travel/__tests__/fixtures'

type Item = { id: string; name: string }
type Page = { items: Item[]; next_cursor: string | null }
const scopes: EffectScope[] = []
function run<T>(callback: () => T): T {
  const scope = effectScope()
  scopes.push(scope)
  return scope.run(callback)!
}
afterEach(() => {
  scopes.splice(0).forEach((scope) => scope.stop())
})

describe('旅行游标列表并发', () => {
  it('筛选变化立即清空游标，忽略迟到的旧首屏结果', async () => {
    const initial = deferred<Page>()
    const filtered = deferred<Page>()
    const query = reactive({ q: '', phase: '' })
    const fetchPage = vi
      .fn()
      .mockReturnValueOnce(initial.promise)
      .mockReturnValueOnce(filtered.promise)
    const page = run(() => useCursorPage<Item, typeof query>(() => ({ ...query }), fetchPage))
    query.q = '东京'
    expect(fetchPage).toHaveBeenCalledTimes(2)
    expect(fetchPage.mock.calls[1]![0]).toEqual({ q: '东京', phase: '' })
    filtered.resolve({ items: [{ id: 'new', name: '东京' }], next_cursor: 'new-cursor' })
    await filtered.promise
    initial.resolve({ items: [{ id: 'old', name: '旧旅行' }], next_cursor: 'old-cursor' })
    await initial.promise
    expect(page.items.value.map((item) => item.id)).toEqual(['new'])
    expect(page.cursor.value).toBe('new-cursor')
    expect(page.loading.value).toBe(false)
  })

  it('下一页并发遇到筛选变化，不把旧页混入新列表', async () => {
    const next = deferred<Page>()
    const refreshed = deferred<Page>()
    const query = reactive({ archived: 'all', sort: 'start_date_desc' })
    const fetchPage = vi
      .fn()
      .mockResolvedValueOnce({ items: [{ id: 'first', name: '旅行一' }], next_cursor: 'cursor-1' })
      .mockReturnValueOnce(next.promise)
      .mockReturnValueOnce(refreshed.promise)
    const page = run(() => useCursorPage<Item, typeof query>(() => ({ ...query }), fetchPage))
    await Promise.resolve()
    const load = page.loadMore()
    expect(fetchPage.mock.calls[1]![0]).toMatchObject({ cursor: 'cursor-1' })
    query.sort = 'updated_at_desc'
    expect(page.cursor.value).toBeNull()
    expect(page.items.value).toEqual([])
    expect(fetchPage.mock.calls[2]![0]).not.toHaveProperty('cursor')
    refreshed.resolve({ items: [{ id: 'fresh', name: '最近更新' }], next_cursor: null })
    await refreshed.promise
    next.resolve({ items: [{ id: 'stale', name: '旧下一页' }], next_cursor: 'cursor-2' })
    await load
    expect(page.items.value.map((item) => item.id)).toEqual(['fresh'])
    expect(page.cursor.value).toBeNull()
  })

  it('下一页失败保留已有项目与游标，重试去重并阻止重复加载', async () => {
    const next = deferred<Page>()
    const fetchPage = vi
      .fn()
      .mockResolvedValueOnce({ items: [{ id: '1', name: '第一趟' }], next_cursor: 'cursor-1' })
      .mockRejectedValueOnce(new TypeError('network'))
      .mockReturnValueOnce(next.promise)
    const page = run(() => useCursorPage<Item, { limit: number }>(() => ({ limit: 30 }), fetchPage))
    await Promise.resolve()
    await page.loadMore()
    expect(page.items.value).toHaveLength(1)
    expect(page.moreError.value).toContain('下一页加载失败')
    expect(page.cursor.value).toBe('cursor-1')
    const retry = page.loadMore()
    await page.loadMore()
    expect(fetchPage).toHaveBeenCalledTimes(3)
    next.resolve({
      items: [
        { id: '1', name: '第一趟' },
        { id: '2', name: '第二趟' },
      ],
      next_cursor: null,
    })
    await retry
    expect(page.items.value.map((item) => item.id)).toEqual(['1', '2'])
    expect(page.moreError.value).toBeNull()
  })

  it('页面离开后完成的请求不再写入列表', async () => {
    const pending = deferred<Page>()
    const page = run(() =>
      useCursorPage<Item, object>(
        () => ({}),
        () => pending.promise,
      ),
    )
    scopes[0]!.stop()
    pending.resolve({ items: [{ id: '1', name: '迟到的数据' }], next_cursor: null })
    await pending.promise
    expect(page.items.value).toEqual([])
  })
})
