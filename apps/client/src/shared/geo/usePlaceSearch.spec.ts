import { effectScope } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/shared/api/auth'
import { searchPlaces, type GeoPlace } from '@/shared/api/geo'
import { usePlaceSearch } from '@/shared/geo/usePlaceSearch'

vi.mock('@/shared/api/geo', () => ({ searchPlaces: vi.fn() }))
const scope = () => effectScope()
const pendingScopes: ReturnType<typeof scope>[] = []
const place = (name: string): GeoPlace => ({
  name,
  address: '',
  latitude: 39.9,
  longitude: 116.4,
  adcode: null,
  poi_id: null,
  provider: 'amap',
})
const tick = async () => {
  await Promise.resolve()
  await Promise.resolve()
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.mocked(searchPlaces).mockReset()
})
afterEach(() => {
  pendingScopes.forEach((s) => s.stop())
  pendingScopes.length = 0
  vi.useRealTimers()
})

function setup() {
  const s = scope()
  pendingScopes.push(s)
  return s.run(() => usePlaceSearch('北京'))!
}

describe('高德地点搜索', () => {
  it('输入防抖，回车式显式搜索取消定时器，不重复消耗配额', async () => {
    vi.mocked(searchPlaces).mockResolvedValue([place('故宫')])
    const search = setup()
    search.keyword.value = '故宫'
    await vi.advanceTimersByTimeAsync(300)
    expect(searchPlaces).not.toHaveBeenCalled()
    await search.search()
    await vi.advanceTimersByTimeAsync(500)
    expect(searchPlaces).toHaveBeenCalledTimes(1)
    expect(search.results.value[0]?.name).toBe('故宫')
  })

  it('新关键词立刻作废旧请求，慢响应不能覆盖新地点', async () => {
    let completeOld!: (value: GeoPlace[]) => void
    vi.mocked(searchPlaces)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            completeOld = resolve
          }),
      )
      .mockResolvedValueOnce([place('颐和园')])
    const search = setup()
    search.keyword.value = '故宫'
    const first = search.search()
    const firstSignal = vi.mocked(searchPlaces).mock.calls[0]?.[1]
    search.keyword.value = '颐和园'
    expect(firstSignal?.aborted).toBe(true)
    await search.search()
    completeOld([place('故宫')])
    await first
    expect(search.results.value[0]?.name).toBe('颐和园')
  })

  it('无结果不算服务故障，清空关键词取消请求并清除结果', async () => {
    vi.mocked(searchPlaces).mockRejectedValue(
      new ApiError({
        code: 'RESOURCE_NOT_FOUND',
        status: 404,
        title: '无结果',
        type: 'about:blank',
        request_id: 'test',
      }),
    )
    const search = setup()
    search.keyword.value = '不存在'
    await search.search()
    expect(search.error.value).toBeNull()
    expect(search.searched.value).toBe(true)
    search.keyword.value = ''
    await tick()
    expect(search.results.value).toEqual([])
    expect(search.searched.value).toBe(false)
  })
})
