import { describe, expect, it } from 'vitest'

import { writeWarnings } from '@/shared/api/writes'
import { receipt } from '@/shared/travel/__tests__/fixtures'

/** 与 apps/server/internal/foundation/write/write.go 的警告代码清单保持一致。 */
const SERVER_WARNINGS = [
  'MERGED_WITH_NEWER_VERSION',
  'REFUNDS_UNLINKED',
  'ITINERARY_OUTSIDE_TRIP_DATES',
  'TIMEZONE_INTERPRETATION_CHANGED',
]

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
