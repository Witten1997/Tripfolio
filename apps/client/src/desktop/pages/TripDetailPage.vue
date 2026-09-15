<script setup lang="ts">
import { ElAlert, ElButton, ElCard, ElSkeleton, ElTag } from 'element-plus'
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'

import IconAction from '@/desktop/components/IconAction.vue'
import TripEditorDialog from '@/desktop/components/TripEditorDialog.vue'
import type { Trip } from '@/shared/api/trips'
import { writeWarnings, type WriteOutcome } from '@/shared/api/writes'
import { useMetadataStore } from '@/shared/stores/metadata'
import { provideTripContext } from '@/shared/travel/tripContext'
import { todayIn } from '@/shared/travel/tripDays'

const route = useRoute()
const metadata = useMetadataStore()
const tripId = String(route.params.tripId)
const context = provideTripContext(tripId)
const { trip, loading, error, errorCode } = context
const editor = ref<InstanceType<typeof TripEditorDialog>>()
const feedback = ref<string[]>([])
const feedbackType = ref<'success' | 'warning'>('success')

const tabs = [
  { name: 'trip-itinerary', label: '行程' },
  { name: 'trip-ledger', label: '账单' },
  { name: 'trip-map', label: '地图' },
  { name: 'trip-packing', label: '行李清单' },
  { name: 'trip-album', label: '相册' },
  { name: 'trip-todos', label: '待办' },
] as const

const phase = computed(() => {
  if (!trip.value) return null
  const today = todayIn(trip.value.timezone)
  if (today < trip.value.start_date) return { label: '待出发', type: 'primary' as const }
  if (today > trip.value.end_date) return { label: '已结束', type: 'info' as const }
  return { label: '旅行中', type: 'success' as const }
})
const gone = computed(() => errorCode.value === 'TRIP_DELETED')

async function saved(outcome: WriteOutcome<Trip>) {
  const warnings = writeWarnings(outcome.result)
  feedbackType.value = warnings.length ? 'warning' : 'success'
  feedback.value = ['旅行已保存。', ...warnings]
  if (outcome.resource) context.replace(outcome.resource)
  else await context.reload()
}

onMounted(() => {
  if (metadata.status === 'idle' || metadata.status === 'error') void metadata.load()
  void context.reload()
})
</script>

<template>
  <div class="trip-detail">
    <ElSkeleton v-if="loading && !trip" :rows="4" animated class="detail-skeleton" />
    <ElCard v-else-if="error && !trip" shadow="never" class="detail-error">
      <ElAlert :title="error" type="error" :closable="false" show-icon />
      <div class="detail-error-actions">
        <RouterLink :to="{ name: 'trips' }"><ElButton>返回旅行列表</ElButton></RouterLink>
        <RouterLink v-if="gone" :to="{ name: 'recycle-bin' }"
          ><ElButton type="primary">前往回收站</ElButton></RouterLink
        >
        <ElButton v-else @click="context.reload">重新加载</ElButton>
      </div>
    </ElCard>
    <template v-else-if="trip">
      <header class="detail-heading">
        <div class="detail-title">
          <RouterLink :to="{ name: 'trips' }" class="detail-back">← 我的旅行</RouterLink>
          <h1>
            {{ trip.name }}
            <ElTag v-if="phase" :type="phase.type" effect="plain" size="small">{{
              phase.label
            }}</ElTag>
            <ElTag v-if="trip.archived_at" type="info" size="small">已归档</ElTag>
          </h1>
          <p class="detail-meta">
            <span>{{ trip.start_date }} 至 {{ trip.end_date }}</span>
            <span>{{ trip.destination || '目的地待定' }}</span>
            <span>{{ trip.timezone }} · {{ trip.currency_code }}</span>
          </p>
        </div>
        <div class="detail-actions tf-actions">
          <IconAction icon="refresh" label="刷新旅行" :loading="loading" @click="context.reload" />
          <IconAction icon="edit" label="编辑旅行" type="primary" @click="editor?.open(trip)" />
        </div>
      </header>
      <ElAlert
        v-if="feedback.length"
        :type="feedbackType"
        :title="feedback.join(' ')"
        show-icon
        @close="feedback = []"
      />
      <nav class="detail-tabs" aria-label="旅行内容页签">
        <RouterLink
          v-for="tab in tabs"
          :key="tab.name"
          :to="{ name: tab.name, params: { tripId } }"
          class="detail-tab"
          active-class="detail-tab--active"
          >{{ tab.label }}</RouterLink
        >
      </nav>
      <RouterView />
    </template>
    <TripEditorDialog ref="editor" @saved="saved" />
  </div>
</template>

<style scoped>
.trip-detail {
  max-width: 1240px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.detail-skeleton {
  padding: 24px;
  background: var(--tf-surface);
  border-radius: var(--tf-radius-control);
}
.detail-error-actions {
  display: flex;
  gap: 8px;
  margin-top: 16px;
}
.detail-error-actions .el-button {
  margin-left: 0;
}
.detail-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
  padding: 8px 0 0;
}
.detail-back {
  font-size: 13px;
  color: var(--tf-text-3);
  text-decoration: none;
}
.detail-back:hover {
  color: var(--tf-accent);
}
.detail-title h1 {
  margin: 6px 0 8px;
  font-size: 28px;
  color: var(--tf-text-1);
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  overflow-wrap: anywhere;
}
.detail-meta {
  margin: 0;
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  color: var(--tf-text-3);
  font-size: 13px;
}
.detail-actions {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  gap: 8px;
}
.detail-actions .el-button {
  margin-left: 0;
}
.detail-tabs {
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--tf-line);
}
.detail-tab {
  padding: 10px 18px;
  white-space: nowrap;
  text-decoration: none;
  color: var(--tf-text-2);
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  transition:
    color var(--tf-duration-fast) var(--tf-ease),
    border-color var(--tf-duration-fast) var(--tf-ease);
}
.detail-tab:hover {
  color: var(--tf-text-1);
}
.detail-tab--active {
  color: var(--tf-accent);
  border-bottom-color: var(--tf-accent);
  font-weight: 600;
}
@media (max-width: 700px) {
  .detail-heading {
    flex-direction: column;
  }
  .detail-tabs {
    overflow-x: auto;
    scrollbar-width: none;
  }
}
</style>
