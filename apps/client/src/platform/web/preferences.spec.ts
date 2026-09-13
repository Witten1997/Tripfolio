import { beforeEach, describe, expect, it } from 'vitest'

import { webPreferences } from './preferences'

describe('webPreferences', () => {
  beforeEach(() => window.localStorage.clear())

  it('写入后可读出，删除后为 null', async () => {
    expect(await webPreferences.get('k')).toBeNull()
    await webPreferences.set('k', 'v')
    expect(await webPreferences.get('k')).toBe('v')
    await webPreferences.remove('k')
    expect(await webPreferences.get('k')).toBeNull()
  })
})
