import { BedDouble, BusFront, CircleEllipsis, Landmark, Utensils } from '@lucide/vue'
import type { Component } from 'vue'

import type { ItineraryKind } from './itineraryKinds'

export const itineraryKindIcons = {
  attraction: Landmark,
  transport: BusFront,
  lodging: BedDouble,
  dining: Utensils,
  other: CircleEllipsis,
} satisfies Record<ItineraryKind, Component>
