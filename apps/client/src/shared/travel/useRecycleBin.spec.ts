import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'

import { useSessionStore } from '@/shared/stores/session'
import { canRestoreTrip, retentionLabel, useRecycleBin } from '@/shared/travel/useRecycleBin'
import { json, problem, receipt, trip } from '@/shared/travel/__tests__/fixtures'

const { transport, refresh } = vi.hoisted(() => ({
  transport: vi.fn<(request: Request) => Promise<Response>>(),
  refresh: vi.fn(async () => false),
}))
vi.mock('@/shared/api/client', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/shared/api/client')>()
  return {
    ...original,
    api: original.createApiClient({ baseUrl: 'http://api.test/api/v1', fetch: transport, refresh }),
  }
})

const scopes: EffectScope[] = []
const trashed = () =>
  trip({ deleted_at: '2026-09-01T00:00:00Z', purge_after_at: '2099-10-01T00:00:00Z' })
function setup() {
  const scope = effectScope()
  scopes.push(scope)
  return scope.run(useRecycleBin)!
}
beforeEach(() => {
  setActivePinia(createPinia())
  transport.mockReset()
  refresh.mockClear()
})
afterEach(() => {
  scopes.splice(0).forEach((scope) => scope.stop())
})

describe('回收站与永久清理安全流程', () => {
  it('按截止时间与清理请求状态判断恢复，不因清理尚未执行而开放恢复', () => {
    const row = trashed()
    expect(canRestoreTrip(row)).toBe(true)
    expect(canRestoreTrip({ ...row, purge_requested_at: '2026-09-02T00:00:00Z' })).toBe(false)
    expect(canRestoreTrip({ ...row, purge_after_at: '2000-01-01T00:00:00Z' })).toBe(false)
    expect(retentionLabel(row, new Date('2099-09-30T23:30:00Z').valueOf())).toBe('剩余约 1 小时')
  })

  it('恢复请求带版本与幂等号，接收 bare page 并刷新列表', async () => {
    const requests: Request[] = []
    let restored = false
    transport.mockImplementation(async (request) => {
      requests.push(request.clone())
      if (request.method === 'POST') {
        restored = true
        return json(200, { data: receipt(trip()) })
      }
      return json(200, { items: restored ? [] : [trashed()], next_cursor: null })
    })
    const recycle = setup()
    await recycle.reload()
    expect(recycle.items.value).toHaveLength(1)
    expect(await recycle.restore(trashed())).toBe(true)
    const write = requests.find((request) => request.method === 'POST')!
    expect(new URL(write.url).pathname).toContain('/restore')
    expect(write.headers.get('If-Match')).toBe('"3"')
    expect(write.headers.get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    expect(recycle.items.value).toEqual([])
    expect(
      await recycle.restore({ ...trashed(), purge_requested_at: '2026-09-02T00:00:00Z' }),
    ).toBe(false)
    expect(requests.filter((request) => request.method === 'POST')).toHaveLength(1)
  })

  it('未明确确认或未输入密码时不发起复验或清理', async () => {
    transport.mockImplementation(async (request) =>
      new URL(request.url).pathname.endsWith('/trips')
        ? json(200, { items: [trashed()], next_cursor: null })
        : json(200, { data: trashed() }),
    )
    const recycle = setup()
    await recycle.openPurge(trashed())
    recycle.password.value = 'current-password'
    expect(await recycle.purge()).toBe(false)
    recycle.confirmed.value = true
    recycle.password.value = ''
    expect(await recycle.purge()).toBe(false)
    expect(transport.mock.calls.every(([request]) => request.method === 'GET')).toBe(true)
  })

  it('错误密码不清空会话、不刷新令牌，也不发送清理请求', async () => {
    useSessionStore().setAccessToken('session-token', 900)
    const methods: string[] = []
    transport.mockImplementation(async (request) => {
      methods.push(new URL(request.url).pathname)
      if (request.method === 'POST') return problem('INVALID_CREDENTIALS', 401)
      return new URL(request.url).pathname.endsWith('/trips')
        ? json(200, { items: [trashed()], next_cursor: null })
        : json(200, { data: trashed() })
    })
    const recycle = setup()
    await recycle.openPurge(trashed())
    recycle.confirmed.value = true
    recycle.password.value = 'wrong-password'
    expect(await recycle.purge()).toBe(false)
    expect(refresh).not.toHaveBeenCalled()
    expect(useSessionStore().accessToken).toBe('session-token')
    expect(methods.some((path) => path.endsWith('/purge'))).toBe(false)
    expect(recycle.password.value).toBe('')
    expect(recycle.purgeOpened.value).toBe(true)
  })

  it('先复验密码再请求清理；202 后保留等待清理记录并禁用恢复', async () => {
    const writes: Request[] = []
    let requested = false
    transport.mockImplementation(async (request) => {
      const path = new URL(request.url).pathname
      if (request.method === 'POST') {
        writes.push(request.clone())
        if (path.endsWith('/reauthenticate')) return new Response(null, { status: 204 })
        requested = true
        return json(202, {
          data: receipt({ ...trashed(), version: '4', purge_requested_at: '2026-09-03T00:00:00Z' }),
        })
      }
      const row = requested
        ? { ...trashed(), version: '4', purge_requested_at: '2026-09-03T00:00:00Z' }
        : trashed()
      return path.endsWith('/trips')
        ? json(200, { items: [row], next_cursor: null })
        : json(200, { data: row })
    })
    const recycle = setup()
    await recycle.openPurge(trashed())
    recycle.confirmed.value = true
    recycle.password.value = 'current-password'
    expect(await recycle.purge()).toBe(true)
    expect(new URL(writes[0]!.url).pathname).toBe('/api/v1/auth/reauthenticate')
    expect(await writes[0]!.json()).toEqual({ password: 'current-password' })
    expect(await writes[1]!.json()).toEqual({ confirm: true })
    expect(writes[1]!.headers.get('If-Match')).toBe('"3"')
    expect(writes[1]!.headers.get('Idempotency-Key')).toMatch(/^[0-9a-f-]{36}$/)
    expect(recycle.items.value).toHaveLength(1)
    expect(recycle.items.value[0]?.purge_requested_at).toBeTruthy()
    expect(recycle.feedback.value).toContain('等待清理完成')
    expect(recycle.password.value).toBe('')
    expect(recycle.purgeOpened.value).toBe(false)
    expect(await recycle.restore(recycle.items.value[0]!)).toBe(false)
    expect(writes).toHaveLength(2)
  })

  it('清理请求网络失败后重试沿用操作号，成功回执没有资源也不崩溃', async () => {
    const writes: Request[] = []
    let attempts = 0
    transport.mockImplementation(async (request) => {
      const path = new URL(request.url).pathname
      if (path.endsWith('/reauthenticate')) return new Response(null, { status: 204 })
      if (path.endsWith('/purge')) {
        writes.push(request.clone())
        if (attempts++ === 0) throw new TypeError('connection reset')
        return json(202, { data: receipt(null, [], true) })
      }
      return path.endsWith('/trips')
        ? json(200, { items: attempts > 1 ? [] : [trashed()], next_cursor: null })
        : json(200, { data: trashed() })
    })
    const recycle = setup()
    await recycle.openPurge(trashed())
    recycle.confirmed.value = true
    recycle.password.value = 'current-password'
    expect(await recycle.purge()).toBe(false)
    expect(recycle.password.value).toBe('')
    recycle.password.value = 'current-password'
    expect(await recycle.purge()).toBe(true)
    expect(writes[0]!.headers.get('Idempotency-Key')).toBe(
      writes[1]!.headers.get('Idempotency-Key'),
    )
    expect(recycle.feedback.value).toContain('请求已受理')
  })

  it('清理遇到版本冲突必须查看最新记录并重新确认', async () => {
    let conflict = false
    let purges = 0
    transport.mockImplementation(async (request) => {
      const path = new URL(request.url).pathname
      if (path.endsWith('/reauthenticate')) return new Response(null, { status: 204 })
      if (path.endsWith('/purge')) {
        purges++
        conflict = true
        return problem('VERSION_CONFLICT', 412)
      }
      return path.endsWith('/trips')
        ? json(200, { items: [trashed()], next_cursor: null })
        : json(200, { data: conflict ? { ...trashed(), version: '4' } : trashed() })
    })
    const recycle = setup()
    await recycle.openPurge(trashed())
    recycle.confirmed.value = true
    recycle.password.value = 'current-password'
    await recycle.purge()
    expect(recycle.selected.value?.version).toBe('4')
    expect(recycle.confirmed.value).toBe(false)
    expect(recycle.password.value).toBe('')
    recycle.password.value = 'current-password'
    await recycle.purge()
    expect(purges).toBe(1)
  })
})
