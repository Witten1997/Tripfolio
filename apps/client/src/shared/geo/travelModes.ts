import type { components } from '@tripfolio/contracts/openapi/v1'

export type TravelMode = components['schemas']['GeoTravelMode']

export const travelModeLabels: Record<TravelMode, string> = {
  driving: '驾车',
  walking: '步行',
  cycling: '骑行',
}
