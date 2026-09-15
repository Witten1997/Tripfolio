import type { components } from '@tripfolio/contracts/openapi/v1'

export type ItineraryKind = components['schemas']['ItineraryKind']

export const itineraryKindLabels: Record<ItineraryKind, string> = {
  attraction: '景点',
  transport: '交通',
  lodging: '住宿',
  dining: '餐饮',
  other: '其他',
}
