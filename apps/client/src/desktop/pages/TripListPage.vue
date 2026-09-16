<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCard,
  ElEmpty,
  ElInput,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSkeleton,
  ElTag,
} from 'element-plus'
import { MapPin, Route as RouteIcon } from '@lucide/vue'
import { computed, nextTick, onMounted, reactive, ref } from 'vue'

import CategoryManagerDialog from '@/desktop/components/CategoryManagerDialog.vue'
import IconAction from '@/desktop/components/IconAction.vue'
import TripEditorDialog from '@/desktop/components/TripEditorDialog.vue'
import { ApiError } from '@/shared/api/auth'
import {
  archiveTrip,
  listTrips,
  trashTrip,
  type Trip,
  type TripListItem,
  type TripQuery,
} from '@/shared/api/trips'
import {
  actionError,
  createWriteIntent,
  writeWarnings,
  type WriteOutcome,
} from '@/shared/api/writes'
import { useMetadataStore } from '@/shared/stores/metadata'
import { useCursorPage } from '@/shared/travel/useCursorPage'

const metadata = useMetadataStore()
const editor = ref<InstanceType<typeof TripEditorDialog>>()
const categoryManager = ref<InstanceType<typeof CategoryManagerDialog>>()
const search = ref('')
const searchInput = ref<InstanceType<typeof ElInput>>()
const activeTool = ref<'search' | 'filters' | null>(null)
const filters = reactive({
  phase: '' as '' | TripListItem['phase'],
  archived: 'all' as NonNullable<TripQuery['archived']>,
  sort: 'start_date_desc' as NonNullable<TripQuery['sort']>,
  q: '',
})
const { items, cursor, loading, loadingMore, error, moreError, reload, loadMore } = useCursorPage(
  () => ({ ...filters, phase: filters.phase || undefined, q: filters.q || undefined, limit: 30 }),
  listTrips,
)
const phases = [
  { key: 'planned', label: '待出发', type: 'primary' },
  { key: 'ongoing', label: '旅行中', type: 'success' },
  { key: 'ended', label: '已结束', type: 'info' },
] as const
const groups = computed(() =>
  phases
    .map((phase) => ({ ...phase, trips: items.value.filter((trip) => trip.phase === phase.key) }))
    .filter((group) => group.trips.length),
)
const isFiltered = computed(() => !!filters.q || !!filters.phase || filters.archived !== 'all')
const feedback = ref<string[]>([])
const feedbackType = ref<'success' | 'warning'>('success')
const actionFailure = ref<string | null>(null)
const busy = ref<string | null>(null)
const intents = new Map<string, ReturnType<typeof createWriteIntent>>()
const sortAscending = computed(() => filters.sort === 'start_date_asc')

async function toggleTool(tool: 'search' | 'filters') {
  activeTool.value = activeTool.value === tool ? null : tool
  if (activeTool.value === 'search') {
    await nextTick()
    searchInput.value?.focus()
  }
}

function toggleSort() {
  filters.sort = sortAscending.value ? 'start_date_desc' : 'start_date_asc'
}

function applySearch() {
  const next = search.value.trim()
  if (filters.q === next) void reload()
  else filters.q = next
}

function clearFilters() {
  search.value = ''
  Object.assign(filters, { q: '', phase: '', archived: 'all' })
}

function formatTripDate(date: string) {
  return date
    .split('-')
    .map((part) => String(Number(part)))
    .join('.')
}

function tripDuration(startDate: string, endDate: string) {
  const start = Date.parse(`${startDate}T00:00:00Z`)
  const end = Date.parse(`${endDate}T00:00:00Z`)
  if (!Number.isFinite(start) || !Number.isFinite(end)) return '—'
  const days = Math.max(1, Math.round((end - start) / 86_400_000) + 1)
  return days === 1 ? '1天' : `${days}天${days - 1}晚`
}

async function saved(outcome: WriteOutcome<Trip>) {
  const warnings = writeWarnings(outcome.result)
  feedbackType.value = warnings.length ? 'warning' : 'success'
  feedback.value = ['旅行已保存。', ...warnings]
  actionFailure.value = null
  await reload()
}

async function mutate(trip: TripListItem, action: 'archive' | 'trash') {
  if (busy.value) return
  busy.value = trip.id
  actionFailure.value = null
  const slot = `${action}:${trip.id}`
  const intent = intents.get(slot) ?? createWriteIntent()
  intents.set(slot, intent)
  const archived = !trip.archived_at
  const operationId = intent.key({
    action,
    id: trip.id,
    version: trip.version,
    archived: action === 'archive' ? archived : undefined,
  })
  try {
    const outcome =
      action === 'archive'
        ? await archiveTrip(trip.id, trip.version, archived, operationId)
        : await trashTrip(trip.id, trip.version, operationId)
    intent.reset()
    const warnings = writeWarnings(outcome.result)
    feedbackType.value = warnings.length ? 'warning' : 'success'
    feedback.value = [
      action === 'trash'
        ? '整趟旅行已移入回收站，可在截止时间前恢复。'
        : archived
          ? '旅行已归档，仍可编辑。'
          : '已取消归档。',
      ...warnings,
    ]
    await reload()
  } catch (cause) {
    actionFailure.value = actionError(
      cause,
      '网络连接中断，结果尚未确认。可重试同一操作或刷新列表确认状态。',
    )
    if (
      cause instanceof ApiError &&
      ['VERSION_CONFLICT', 'TRIP_DELETED'].includes(cause.code ?? '')
    )
      await reload()
  } finally {
    busy.value = null
  }
}

async function confirmTrash(trip: TripListItem) {
  if (busy.value) return
  try {
    await ElMessageBox.confirm(
      `将“${trip.name}”及其行程、账目、行李、待办、预订资料和照片一起移入回收站。30 天内可恢复整趟旅行。`,
      '将整趟旅行移入回收站？',
      {
        type: 'warning',
        confirmButtonText: '确认移入回收站',
        cancelButtonText: '保留旅行',
      },
    )
  } catch {
    return
  }
  await mutate(trip, 'trash')
}

onMounted(() => {
  if (metadata.status === 'idle' || metadata.status === 'error') void metadata.load()
})

function openCreate() {
  editor.value?.open()
}

defineExpose({ openCreate })
</script>

<template>
  <div class="trip-list-page">
    <header class="page-heading">
      <div>
        <h1>我的旅行</h1>
        <p>每一次出发，都值得好好记录。</p>
      </div>
      <div class="heading-actions tf-actions">
        <div class="heading-primary-actions">
          <ElButton @click="categoryManager?.open()">账单分类</ElButton
          ><ElButton type="primary" @click="editor?.open()">新建旅行</ElButton>
        </div>
        <div class="heading-tool-actions" aria-label="旅行列表工具">
          <IconAction
            icon="search"
            label="搜索旅行"
            appearance="ghost"
            :aria-expanded="activeTool === 'search'"
            aria-controls="trip-search-panel"
            :class="{ 'is-active': activeTool === 'search' }"
            @click="toggleTool('search')"
          />
          <IconAction
            icon="filter"
            label="筛选旅行"
            appearance="ghost"
            :aria-expanded="activeTool === 'filters'"
            aria-controls="trip-filter-panel"
            :class="{ 'is-active': activeTool === 'filters' }"
            @click="toggleTool('filters')"
          />
          <IconAction
            :icon="sortAscending ? 'sort-asc' : 'sort-desc'"
            appearance="ghost"
            :label="
              sortAscending
                ? '当前按出发日期升序，点击切换为降序'
                : '当前按出发日期降序，点击切换为升序'
            "
            @click="toggleSort"
          />
        </div>
      </div>
    </header>
    <Transition name="tool-panel">
      <form
        v-if="activeTool === 'search'"
        id="trip-search-panel"
        class="trip-tool-panel search-panel"
        @submit.prevent="applySearch"
      >
        <ElInput
          id="trip-search"
          ref="searchInput"
          v-model="search"
          aria-label="搜索旅行"
          placeholder="旅行名称或目的地"
          maxlength="100"
          clearable
          @clear="applySearch"
        />
        <IconAction icon="search" label="执行搜索" appearance="ghost" @click="applySearch" />
      </form>
      <section
        v-else-if="activeTool === 'filters'"
        id="trip-filter-panel"
        class="trip-tool-panel filter-panel"
        aria-label="筛选旅行"
      >
        <div class="filter-field">
          <label for="phase-filter">旅行阶段</label
          ><ElSelect
            id="phase-filter"
            v-model="filters.phase"
            aria-label="旅行阶段"
            placeholder="全部阶段"
            ><ElOption label="全部阶段" value="" /><ElOption
              v-for="phase in phases"
              :key="phase.key"
              :label="phase.label"
              :value="phase.key"
          /></ElSelect>
        </div>
        <div class="filter-field">
          <label for="archive-filter">归档状态</label
          ><ElSelect id="archive-filter" v-model="filters.archived" aria-label="归档状态"
            ><ElOption label="全部旅行" value="all" /><ElOption
              label="未归档"
              value="false" /><ElOption label="已归档" value="true"
          /></ElSelect>
        </div>
      </section>
    </Transition>
    <ElAlert
      v-if="feedback.length"
      :type="feedbackType"
      :title="feedback.join(' ')"
      show-icon
      @close="feedback = []"
    />
    <ElAlert v-if="actionFailure" type="error" :title="actionFailure" :closable="false" show-icon />
    <ElSkeleton v-if="loading" :rows="7" animated class="list-skeleton" />
    <ElCard v-else-if="error" shadow="never"
      ><ElAlert :title="error" type="error" :closable="false" show-icon /><ElButton
        class="retry-button"
        @click="reload"
        >重新加载</ElButton
      ></ElCard
    >
    <ElCard v-else-if="!items.length" shadow="never"
      ><ElEmpty
        :description="isFiltered ? '没有符合条件的旅行' : '还没有旅行，开始计划下一次出发吧'"
      >
        <ElButton v-if="isFiltered" @click="clearFilters">清除筛选</ElButton
        ><ElButton v-else type="primary" @click="editor?.open()">新建旅行</ElButton>
      </ElEmpty></ElCard
    >
    <template v-else>
      <section
        v-for="group in groups"
        :key="group.key"
        class="trip-group"
        :aria-labelledby="`phase-${group.key}`"
      >
        <h2 :id="`phase-${group.key}`">
          <ElTag :type="group.type" effect="plain">{{ group.label }}</ElTag
          ><span>{{ group.trips.length }} 趟</span>
        </h2>
        <div class="trip-grid">
          <ElCard
            v-for="trip in group.trips"
            :key="trip.id"
            shadow="hover"
            class="trip-card tf-card"
          >
            <div class="trip-card-layout">
              <div class="trip-card-main">
                <div class="trip-card-title">
                  <MapPin class="trip-title-icon" aria-hidden="true" />
                  <h3>
                    <RouterLink
                      :to="{ name: 'trip-detail', params: { tripId: trip.id } }"
                      class="trip-link"
                      >{{ trip.name }}</RouterLink
                    >
                  </h3>
                  <ElTag v-if="trip.archived_at" type="info" size="small">已归档</ElTag>
                </div>
                <p class="trip-dates">
                  {{ formatTripDate(trip.start_date) }} - {{ formatTripDate(trip.end_date) }}
                </p>
                <div class="trip-actions tf-actions">
                  <IconAction
                    :to="{ name: 'trip-detail', params: { tripId: trip.id } }"
                    icon="eye"
                    :label="`查看详情：${trip.name}`"
                    appearance="ghost"
                  />
                  <IconAction
                    icon="edit"
                    :label="`编辑旅行：${trip.name}`"
                    appearance="ghost"
                    :disabled="!!busy"
                    @click="editor?.open(trip)"
                  />
                  <IconAction
                    :icon="trip.archived_at ? 'archive-restore' : 'archive'"
                    :label="`${trip.archived_at ? '取消归档' : '归档'}：${trip.name}`"
                    appearance="ghost"
                    :disabled="!!busy && busy !== trip.id"
                    :loading="busy === trip.id"
                    @click="mutate(trip, 'archive')"
                  />
                  <IconAction
                    icon="trash"
                    :label="`移入回收站：${trip.name}`"
                    appearance="ghost"
                    class="trip-action--danger"
                    type="danger"
                    :disabled="!!busy"
                    @click="confirmTrash(trip)"
                  />
                </div>
              </div>
              <aside class="trip-card-summary" :aria-label="`${trip.name}旅行概览`">
                <div class="trip-summary-mark" aria-hidden="true">
                  <span>TRIPFOLIO</span>
                  <RouteIcon />
                </div>
                <dl class="trip-metrics">
                  <div>
                    <dt>时长</dt>
                    <dd>{{ tripDuration(trip.start_date, trip.end_date) }}</dd>
                  </div>
                  <div>
                    <dt>里程</dt>
                    <dd>待计算</dd>
                  </div>
                </dl>
              </aside>
            </div>
          </ElCard>
        </div>
      </section>
      <div class="pagination-area">
        <span>已加载 {{ items.length }} 趟旅行</span
        ><ElAlert v-if="moreError" :title="moreError" type="error" :closable="false" /><ElButton
          v-if="cursor"
          :loading="loadingMore"
          @click="loadMore"
          >{{ moreError ? '重试加载下一页' : '加载更多' }}</ElButton
        ><span v-else>已显示全部旅行</span>
      </div>
    </template>
    <TripEditorDialog ref="editor" @saved="saved" />
    <CategoryManagerDialog ref="categoryManager" />
  </div>
</template>

<style scoped>
.trip-list-page {
  max-width: 1240px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.page-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 8px 0;
}
.page-heading h1 {
  margin: 0;
  font-size: 28px;
  color: var(--tf-text-1);
}
.page-heading p {
  margin: 8px 0 0;
  color: var(--tf-text-3);
}
.heading-actions {
  display: flex;
  flex-shrink: 0;
  gap: 8px;
}
.heading-primary-actions,
.heading-tool-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.heading-primary-actions .el-button {
  margin-left: 0;
}
.heading-tool-actions :deep(.is-active) {
  background: var(--tf-accent-soft);
  color: var(--tf-accent);
}
.trip-tool-panel {
  box-sizing: border-box;
  display: flex;
  align-items: end;
  gap: 12px;
  width: 100%;
  padding: 14px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface);
  box-shadow: var(--tf-shadow-1);
}
.search-panel .el-input {
  flex: 1;
}
.filter-panel {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 220px));
  justify-content: end;
}
.filter-field label {
  display: block;
  margin-bottom: 7px;
  font-size: 12px;
  color: var(--tf-text-2);
}
.filter-field .el-select {
  width: 100%;
}
.tool-panel-enter-active,
.tool-panel-leave-active {
  transition:
    opacity var(--tf-duration-fast) var(--tf-ease),
    transform var(--tf-duration-fast) var(--tf-ease);
}
.tool-panel-enter-from,
.tool-panel-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
.list-skeleton {
  padding: 24px;
  background: var(--tf-surface);
  border-radius: var(--tf-radius-control);
}
.retry-button {
  margin-top: 16px;
}
.trip-group h2 {
  margin: 0 0 14px;
  display: flex;
  gap: 10px;
  align-items: center;
}
.trip-group h2 > span:last-child {
  font-size: 13px;
  font-weight: normal;
  color: var(--tf-text-3);
}
.trip-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(460px, 1fr));
  gap: 16px;
}
.trip-card {
  overflow: hidden;
  transition:
    transform var(--tf-duration) var(--tf-ease),
    box-shadow var(--tf-duration) var(--tf-ease);
}
.trip-card:hover {
  transform: translateY(-2px);
  box-shadow: var(--tf-shadow-2);
}
.trip-card :deep(.el-card__body) {
  padding: 0;
  box-sizing: border-box;
  overflow: visible;
}
.trip-card-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 150px;
  min-height: 220px;
}
.trip-card-main {
  display: flex;
  min-width: 0;
  padding: 24px;
  flex-direction: column;
}
.trip-card-title {
  display: flex;
  align-items: center;
  gap: 10px;
}
.trip-title-icon {
  flex: 0 0 auto;
  width: 23px;
  height: 23px;
  color: var(--tf-warning);
  stroke-width: 2.2;
}
.trip-card h3 {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 21px;
  line-height: 1.35;
  overflow-wrap: anywhere;
}
.trip-link {
  color: var(--tf-text-1);
  text-decoration: none;
}
.trip-link:hover {
  color: var(--tf-accent);
}
.trip-dates {
  margin: 12px 0 0 33px;
  color: var(--tf-text-2);
  font-size: 15px;
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  line-height: 1.5;
}
.trip-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-start;
  gap: 8px;
  margin-top: auto;
  padding-top: 28px;
}
.trip-actions .el-button {
  margin-left: 0;
}
.trip-actions :deep(.trip-action--danger) {
  color: var(--tf-danger);
}
.trip-actions :deep(.trip-action--danger:hover) {
  background: var(--tf-danger-soft);
  color: var(--tf-danger);
}
.trip-card-summary {
  position: relative;
  display: flex;
  min-width: 0;
  padding: 20px 18px;
  flex-direction: column;
  background: var(--tf-warning-soft);
  border-left: 1px dashed color-mix(in srgb, var(--tf-warning) 58%, transparent);
}
.trip-card-summary::before,
.trip-card-summary::after {
  position: absolute;
  left: 0;
  z-index: 1;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: var(--tf-canvas);
  content: '';
}
.trip-card-summary::before {
  top: 0;
  transform: translate(-50%, -50%);
}
.trip-card-summary::after {
  bottom: 0;
  transform: translate(-50%, 50%);
}
.trip-summary-mark {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding-bottom: 13px;
  border-bottom: 1px dashed color-mix(in srgb, var(--tf-text-1) 55%, transparent);
  color: var(--tf-text-2);
  font-size: 11px;
  font-weight: 800;
}
.trip-summary-mark svg {
  width: 20px;
  height: 20px;
}
.trip-metrics {
  display: flex;
  flex: 1;
  margin: 0;
  padding: 15px 0 0;
  flex-direction: column;
  justify-content: space-between;
  gap: 14px;
  text-align: right;
}
.trip-metrics div {
  min-width: 0;
}
.trip-metrics dt {
  color: var(--tf-text-2);
  font-size: 12px;
  font-weight: 600;
}
.trip-metrics dd {
  margin: 3px 0 0;
  color: var(--tf-text-1);
  font-size: 17px;
  font-weight: 800;
  line-height: 1.25;
  overflow-wrap: anywhere;
}
.pagination-area {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  color: var(--tf-text-3);
  font-size: 12px;
  padding: 8px;
}
@media (max-width: 600px) {
  .page-heading {
    align-items: flex-start;
  }
  .trip-grid {
    grid-template-columns: 1fr;
  }
  .trip-card-layout {
    grid-template-columns: minmax(0, 1fr) 116px;
    min-height: 200px;
  }
  .trip-card-main {
    padding: 20px 16px;
  }
  .trip-card-summary {
    padding: 17px 14px;
  }
  .trip-summary-mark span {
    font-size: 9px;
  }
  .trip-card h3 {
    font-size: 19px;
  }
  .trip-dates {
    margin-left: 0;
    font-size: 13px;
  }
  .trip-actions {
    gap: 3px;
    padding-top: 20px;
  }
  .trip-metrics dd {
    font-size: 15px;
  }
  .filter-panel {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
