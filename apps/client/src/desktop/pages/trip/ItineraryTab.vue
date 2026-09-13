<script setup lang="ts">
import { ElAlert, ElButton, ElCard, ElEmpty, ElMessageBox, ElSkeleton, ElTag } from 'element-plus'
import { computed, onMounted, ref, watch } from 'vue'
import { VueDraggable, type DraggableEvent } from 'vue-draggable-plus'

import ItineraryItemDialog from '@/desktop/components/ItineraryItemDialog.vue'
import { ApiError } from '@/shared/api/auth'
import {
  deleteItineraryItem,
  itineraryKindLabels,
  itineraryStatusLabels,
  type ItineraryItem,
} from '@/shared/api/itinerary'
import {
  actionError,
  createWriteIntent,
  writeWarnings,
  type WriteOutcome,
} from '@/shared/api/writes'
import { useTripContext } from '@/shared/travel/tripContext'
import { useItineraryBoard } from '@/shared/travel/useItineraryBoard'

const context = useTripContext()
const trip = context.trip
const board = useItineraryBoard(context.tripId, () => ({
  start: trip.value?.start_date ?? '',
  end: trip.value?.end_date ?? '',
}))
const { days, loading, error, reordering, actionFailure, feedback } = board
const dialog = ref<InstanceType<typeof ItineraryItemDialog>>()
const busy = ref<string | null>(null)
const notice = ref<string[]>([])
const noticeType = ref<'success' | 'warning'>('success')
const intents = new Map<string, ReturnType<typeof createWriteIntent>>()

/**
 * 拖动组件需要可变数组：每天维护一份镜像。VueDraggable 在挂载时读取 v-model，
 * 值不是数组会被当成选项而不初始化，所以镜像必须在首次渲染前就按天铺满（immediate + sync）。
 * 拖动结束后 board 重载，days 变化，镜像随之按服务端结果重建。
 */
const lists = ref<Record<string, ItineraryItem[]>>({})
watch(
  days,
  (next) => {
    lists.value = Object.fromEntries(next.map((d) => [d.date, [...d.items]]))
  },
  { immediate: true, flush: 'sync' },
)
const total = computed(() => board.items.value.length)
const isToday = (date: string) => date === context.today.value

async function reloadAll() {
  await board.reload()
}

function jumpToToday() {
  const el = document.getElementById(`day-${context.today.value}`)
  el?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

async function onDragEnd(event: DraggableEvent<ItineraryItem>) {
  const from = event.from.dataset.date
  const to = event.to.dataset.date
  const id = event.data?.id
  if (!from || !to || !id || event.newIndex === undefined) {
    await reloadAll()
    return
  }
  // moveItem 成功或失败都会重载 board，镜像由 days 的 watcher 重建
  const moved = await board.moveItem(id, to, event.newIndex)
  if (moved && feedback.value.length) {
    noticeType.value = 'warning'
    notice.value = feedback.value
  }
}

async function saved(outcome: WriteOutcome<ItineraryItem>) {
  const warnings = writeWarnings(outcome.result)
  noticeType.value = warnings.length ? 'warning' : 'success'
  notice.value = ['行程已保存。', ...warnings]
  actionFailure.value = null
  await reloadAll()
}

async function remove(item: ItineraryItem) {
  if (busy.value || reordering.value) return
  try {
    await ElMessageBox.confirm(`删除“${item.title}”不影响其他记录。`, '删除这条行程？', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '保留',
    })
  } catch {
    return
  }
  busy.value = item.id
  actionFailure.value = null
  const intent = intents.get(item.id) ?? createWriteIntent()
  intents.set(item.id, intent)
  try {
    await deleteItineraryItem(
      context.tripId,
      item.id,
      item.version,
      intent.key({ delete: item.id, version: item.version }),
    )
    intent.reset()
    noticeType.value = 'success'
    notice.value = ['行程已删除。']
    await reloadAll()
  } catch (cause) {
    actionFailure.value = actionError(cause, '网络连接中断，结果尚未确认。可重试或刷新后确认。')
    if (
      cause instanceof ApiError &&
      ['VERSION_CONFLICT', 'RESOURCE_GONE'].includes(cause.code ?? '')
    )
      await reloadAll()
  } finally {
    busy.value = null
  }
}

function timeLabel(item: ItineraryItem) {
  const start = item.planned_start_local?.slice(11, 16)
  if (item.planned_end_local) {
    const sameDay = item.planned_end_local.slice(0, 10) === item.scheduled_on
    const end = sameDay
      ? item.planned_end_local.slice(11, 16)
      : item.planned_end_local.slice(5, 16).replace('T', ' ')
    return start ? `${start} – ${end}` : `至 ${end}`
  }
  if (item.planned_duration_minutes) {
    return start
      ? `${start} · ${item.planned_duration_minutes} 分钟`
      : `${item.planned_duration_minutes} 分钟`
  }
  return start ?? ''
}

onMounted(async () => {
  await reloadAll()
})
</script>

<template>
  <div class="itinerary-tab">
    <div class="tab-toolbar">
      <span class="tab-summary">
        共 {{ total }} 条行程 · 拖动卡片调整顺序或移到其他日期
        <template v-if="reordering">，正在保存排序…</template>
      </span>
      <div class="tab-actions">
        <ElButton v-if="days.some((d) => isToday(d.date))" size="small" @click="jumpToToday"
          >今日行程</ElButton
        >
        <ElButton size="small" :loading="loading" @click="reloadAll">刷新</ElButton>
        <ElButton size="small" type="primary" @click="dialog?.open(undefined, trip?.start_date)"
          >新建行程</ElButton
        >
      </div>
    </div>
    <ElAlert
      v-if="notice.length"
      :type="noticeType"
      :title="notice.join(' ')"
      show-icon
      @close="notice = []"
    />
    <ElAlert v-if="actionFailure" type="error" :title="actionFailure" :closable="false" show-icon />
    <ElSkeleton v-if="loading && !total" :rows="8" animated class="tab-skeleton" />
    <ElCard v-else-if="error" shadow="never">
      <ElAlert :title="error" type="error" :closable="false" show-icon />
      <ElButton class="retry-button" @click="reloadAll">重新加载</ElButton>
    </ElCard>
    <div v-else class="day-list" :class="{ 'day-list--busy': reordering }">
      <section
        v-for="day in days"
        :id="`day-${day.date}`"
        :key="day.date"
        class="day tf-surface"
        :class="{ 'day--today': isToday(day.date), 'day--outside': day.outside }"
      >
        <header class="day-header">
          <h2>
            {{ day.title }}
            <ElTag v-if="isToday(day.date)" type="success" size="small" effect="plain">今天</ElTag>
            <ElTag v-if="day.outside" type="warning" size="small" effect="plain">旅行日期外</ElTag>
          </h2>
          <span class="day-count">{{ day.items.length }} 项</span>
          <ElButton
            size="small"
            text
            :disabled="reordering"
            @click="dialog?.open(undefined, day.date)"
            >添加</ElButton
          >
        </header>
        <VueDraggable
          v-model="lists[day.date]"
          group="itinerary"
          :data-date="day.date"
          :disabled="reordering || !!busy"
          :animation="150"
          :force-fallback="true"
          handle=".drag-handle"
          class="day-items"
          ghost-class="item--ghost"
          @end="onDragEnd"
        >
          <article
            v-for="item in lists[day.date] ?? []"
            :key="item.id"
            class="item"
            :class="`item--${item.status}`"
          >
            <button
              type="button"
              class="drag-handle"
              :aria-label="`拖动 ${item.title}`"
              :disabled="reordering"
            >
              ⋮⋮
            </button>
            <div class="item-body">
              <div class="item-title">
                <ElTag size="small" effect="plain">{{ itineraryKindLabels[item.kind] }}</ElTag>
                <strong>{{ item.title }}</strong>
                <ElTag
                  v-if="item.status !== 'pending'"
                  size="small"
                  :type="item.status === 'completed' ? 'success' : 'info'"
                  >{{ itineraryStatusLabels[item.status] }}</ElTag
                >
              </div>
              <p v-if="timeLabel(item)" class="item-time">{{ timeLabel(item) }}</p>
              <p v-if="item.place_name || item.address" class="item-place">
                {{ item.place_name }}<span v-if="item.place_name && item.address"> · </span
                >{{ item.address }}
              </p>
              <p v-if="item.estimated_amount" class="item-amount">
                预计 {{ item.currency_code }} {{ item.estimated_amount }}
              </p>
              <p v-if="item.notes" class="item-notes">{{ item.notes }}</p>
            </div>
            <div class="item-actions">
              <ElButton
                size="small"
                text
                :disabled="!!busy || reordering"
                @click="dialog?.open(item)"
                >编辑</ElButton
              >
              <ElButton
                size="small"
                text
                type="danger"
                :loading="busy === item.id"
                :disabled="(!!busy && busy !== item.id) || reordering"
                @click="remove(item)"
                >删除</ElButton
              >
            </div>
          </article>
        </VueDraggable>
        <ElEmpty
          v-if="!lists[day.date]?.length"
          description="这一天还没有安排"
          :image-size="48"
          class="day-empty"
        />
      </section>
    </div>
    <ItineraryItemDialog ref="dialog" @saved="saved" />
  </div>
</template>

<style scoped>
.itinerary-tab {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.tab-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}
.tab-summary {
  font-size: 12px;
  color: var(--tf-text-3);
}
.tab-actions {
  display: flex;
  gap: 8px;
}
.tab-actions .el-button {
  margin-left: 0;
}
.tab-skeleton {
  padding: 24px;
  background: var(--tf-surface);
  border-radius: var(--tf-radius-control);
}
.retry-button {
  margin-top: 16px;
}
.day-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.day-list--busy {
  opacity: 0.7;
  pointer-events: none;
}
.day {
  padding: 16px 18px;
  scroll-margin-top: 80px;
}
.day--today {
  border-color: var(--tf-success);
}
.day--outside {
  border-style: dashed;
}
.day-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}
.day-header h2 {
  margin: 0;
  font-size: 17px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.day-count {
  font-size: 12px;
  color: var(--tf-text-3);
  margin-right: auto;
}
.day-items {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-height: 12px;
}
.item {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  padding: 10px 12px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-raised);
}
.item--completed {
  opacity: 0.75;
}
.item--skipped {
  opacity: 0.6;
}
.item--skipped .item-title strong {
  text-decoration: line-through;
}
.item--ghost {
  opacity: 0.4;
  border-style: dashed;
  border-color: var(--tf-accent);
}
.drag-handle {
  cursor: grab;
  border: 0;
  background: transparent;
  color: var(--tf-text-3);
  font-size: 14px;
  line-height: 1;
  padding: 6px 2px;
  letter-spacing: -2px;
}
.drag-handle:disabled {
  cursor: default;
}
.item-body {
  flex: 1;
  min-width: 0;
}
.item-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.item-title strong {
  overflow-wrap: anywhere;
}
.item-time,
.item-place,
.item-amount,
.item-notes {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--tf-text-2);
  overflow-wrap: anywhere;
}
.item-notes {
  color: var(--tf-text-3);
  white-space: pre-wrap;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.item-actions {
  display: flex;
  flex-shrink: 0;
}
.item-actions .el-button {
  margin-left: 0;
}
.day-empty {
  padding: 4px 0 0;
}
.day-empty :deep(.el-empty__description) {
  margin-top: 4px;
}
</style>
