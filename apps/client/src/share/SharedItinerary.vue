<script setup lang="ts">
import { computed } from 'vue'

import { itineraryKindLabels } from '@/shared/travel/itineraryKinds'
import { groupByDay } from '@/shared/travel/tripDays'

import type { PublicItineraryItem, PublicTrip } from './api'

const props = defineProps<{ trip: PublicTrip; items: PublicItineraryItem[] }>()
const days = computed(() => groupByDay(props.trip.start_date, props.trip.end_date, props.items))

function timeOf(item: PublicItineraryItem): string {
  const start = item.planned_start_local?.slice(11, 16)
  const end = item.planned_end_local?.slice(11, 16)
  if (start && end) return `${start} – ${end}`
  if (start && item.planned_duration_minutes)
    return `${start} · 约 ${item.planned_duration_minutes} 分钟`
  return start ?? ''
}
</script>

<template>
  <ol class="share-days" aria-label="按天行程">
    <li v-for="day in days" :key="day.date" class="share-day tf-surface">
      <h2 class="share-day__title">
        {{ day.title }}<small v-if="day.outside">（旅行日期之外）</small>
      </h2>
      <p v-if="!day.items.length" class="share-day__empty">这一天暂无安排</p>
      <ol v-else class="share-items">
        <li v-for="(item, index) in day.items" :key="item.id" class="share-item">
          <span class="share-item__number" aria-hidden="true">{{ index + 1 }}</span>
          <div class="share-item__body">
            <p class="share-item__title">
              {{ item.title }}
              <span class="share-item__kind">{{ itineraryKindLabels[item.kind] }}</span>
            </p>
            <p v-if="timeOf(item)" class="share-item__time">{{ timeOf(item) }}</p>
            <p v-if="item.place_name || item.address" class="share-item__place">
              {{ item.place_name }}<span v-if="item.address"> · {{ item.address }}</span>
            </p>
          </div>
        </li>
      </ol>
    </li>
  </ol>
</template>

<style scoped>
.share-days,
.share-items {
  margin: 0;
  padding: 0;
  list-style: none;
}
.share-days {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.share-day {
  padding: 16px 18px;
  border-radius: var(--tf-radius-card);
}
.share-day__title {
  margin: 0 0 10px;
  font-size: 16px;
}
.share-day__title small {
  margin-left: 6px;
  font-size: 12px;
  font-weight: normal;
  color: var(--tf-text-3);
}
.share-day__empty {
  margin: 0;
  color: var(--tf-text-3);
}
.share-item {
  display: flex;
  gap: 12px;
  padding: 10px 0;
  border-top: 1px solid var(--tf-line-soft);
}
.share-item__number {
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
  font-size: 12px;
}
.share-item__body p {
  margin: 0;
}
.share-item__title {
  font-weight: 600;
}
.share-item__kind {
  margin-left: 6px;
  padding: 1px 6px;
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-inset);
  font-size: 12px;
  font-weight: normal;
  color: var(--tf-text-2);
}
.share-item__time,
.share-item__place {
  margin-top: 2px;
  font-size: 13px;
  color: var(--tf-text-2);
}
</style>
