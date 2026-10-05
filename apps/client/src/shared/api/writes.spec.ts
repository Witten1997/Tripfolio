import { describe, expect, it } from 'vitest'

import { actionError, writeWarnings } from '@/shared/api/writes'
import { ApiError } from '@/shared/api/problem'
import { receipt } from '@/shared/travel/__tests__/fixtures'

/** 与 apps/server/internal/foundation/write/write.go 的警告代码清单保持一致。 */
const SERVER_WARNINGS = [
  'MERGED_WITH_NEWER_VERSION',
  'REFUNDS_UNLINKED',
  'ITINERARY_OUTSIDE_TRIP_DATES',
  'TIMEZONE_INTERPRETATION_CHANGED',
]

describe('collection error messages', () => {
  it.each(['COLLECTION_BASE_REQUIRED', 'COLLECTION_CONFLICT'])(
    'preserves %s for caller handling and asks to retain input',
    (code) => {
      const error = new ApiError({
        type: 'about:blank',
        title: code,
        status: code === 'COLLECTION_BASE_REQUIRED' ? 428 : 412,
        code,
        request_id: 'h07-w-test',
      })
      expect(actionError(error)).toContain('保留当前输入')
      expect(error.code).toBe(code)
      expect(actionError(error)).toContain(
        code === 'COLLECTION_BASE_REQUIRED' ? '刷新升级' : '核对最新内容',
      )
    },
  )
})

describe('writeWarnings', () => {
  it('服务端的每个警告代码都有中文文案，不把代码直接抛给用户', () => {
    for (const code of SERVER_WARNINGS) {
      const [text] = writeWarnings(receipt(null, [code]))
      expect(text, code).toBeDefined()
      expect(text, code).not.toBe(code)
      expect(text!.length, code).toBeGreaterThan(4)
    }
  })

  it('未登记的代码原样返回，便于发现遗漏', () => {
    expect(writeWarnings(receipt(null, ['SOMETHING_NEW']))).toEqual(['SOMETHING_NEW'])
  })
})
