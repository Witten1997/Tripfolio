<script setup lang="ts">
import { ElAlert, ElButton, ElRadioButton, ElRadioGroup, ElSkeleton } from 'element-plus'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

import type { SharedTravelMode } from './api'
import SharedItinerary from './SharedItinerary.vue'
import SharedMap from './SharedMap.vue'
import { createShareClient } from './shareClient'
import { useSharedTrip } from './useSharedTrip'

const route = useRoute()
// 令牌只在内存中：不写 localStorage，不进入会话 store。
const client = createShareClient(String(route.params.token ?? ''))
const { trip, items, loading, error, routes, routesState, load, loadRoutes } = useSharedTrip(client)
const view = ref<'itinerary' | 'map'>('itinerary')
const mode = ref<SharedTravelMode>('driving')

const errorText = computed(() => {
  switch (error.value) {
    case 'invalid':
      return '这条分享链接已失效。请向分享者索取新的链接。'
    case 'deleted':
      return '这趟旅行已被删除，暂时无法查看。'
    case 'network':
      return '无法加载分享内容，请检查网络后重试。'
    default:
      return null
  }
})

// 行程与地图两个视图都要显示站间距离：拿到行程后即算路，切换出行方式重算。
watch(
  [() => items.value.length, mode],
  () => {
    if (items.value.length >= 2) void loadRoutes(mode.value)
  },
  { immediate: true },
)

onMounted(() => {
  void load()
})
</script>

<template>
  <section class="share-page" aria-label="分享的旅行">
    <ElSkeleton v-if="loading && !trip" :rows="6" animated />
    <div v-else-if="errorText" class="share-state tf-surface">
      <ElAlert
        :title="errorText"
        :type="error === 'network' ? 'error' : 'info'"
        show-icon
        :closable="false"
      />
      <ElButton v-if="error === 'network'" @click="load">重新加载</ElButton>
    </div>
    <template v-else-if="trip">
      <header class="share-heading tf-surface">
        <p class="share-kicker">分享的旅行 · 只读</p>
        <h1>{{ trip.name }}</h1>
        <p class="share-meta">
          <span>{{ trip.start_date }} 至 {{ trip.end_date }}</span>
          <span>{{ trip.destination || '目的地待定' }}</span>
        </p>
        <ElRadioGroup v-model="view" aria-label="查看方式" class="share-view">
          <ElRadioButton value="itinerary">行程</ElRadioButton>
          <ElRadioButton value="map">地图</ElRadioButton>
        </ElRadioGroup>
      </header>
      <SharedItinerary
        v-if="view === 'itinerary'"
        v-model:mode="mode"
        :trip="trip"
        :items="items"
        :routes="routes"
        :routes-state="routesState"
      />
      <SharedMap
        v-else
        v-model:mode="mode"
        :items="items"
        :routes="routes"
        :routes-state="routesState"
      />
    </template>
  </section>
</template>

<style scoped>
.share-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.share-state,
.share-heading {
  padding: 20px;
  border-radius: var(--tf-radius-card);
}
.share-state {
  display: flex;
  flex-direction: column;
  gap: 12px;
  align-items: flex-start;
}
.share-kicker {
  margin: 0 0 4px;
  font-size: 12px;
  letter-spacing: 0.08em;
  color: var(--tf-text-3);
}
.share-heading h1 {
  margin: 0;
  font-size: clamp(22px, 4vw, 30px);
  line-height: 1.25;
}
.share-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
  margin: 8px 0 14px;
  color: var(--tf-text-2);
}
</style>
