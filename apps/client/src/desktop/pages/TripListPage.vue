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
import { computed, onMounted, reactive, ref } from 'vue'

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

function applySearch() {
  const next = search.value.trim()
  if (filters.q === next) void reload()
  else filters.q = next
}

function clearFilters() {
  search.value = ''
  Object.assign(filters, { q: '', phase: '', archived: 'all' })
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
        <ElButton @click="categoryManager?.open()">账单分类</ElButton
        ><ElButton type="primary" @click="editor?.open()">新建旅行</ElButton>
      </div>
    </header>
    <ElCard shadow="never" class="filters-card">
      <form class="trip-filters" @submit.prevent="applySearch">
        <div class="search-filter">
          <label for="trip-search">搜索旅行</label>
          <div class="search-input">
            <ElInput
              id="trip-search"
              v-model="search"
              placeholder="旅行名称或目的地"
              maxlength="100"
              clearable
              @clear="applySearch"
            /><ElButton native-type="submit">搜索</ElButton>
          </div>
        </div>
        <div>
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
        <div>
          <label for="archive-filter">归档状态</label
          ><ElSelect id="archive-filter" v-model="filters.archived" aria-label="归档状态"
            ><ElOption label="全部旅行" value="all" /><ElOption
              label="未归档"
              value="false" /><ElOption label="已归档" value="true"
          /></ElSelect>
        </div>
        <div>
          <label for="sort-filter">排序</label
          ><ElSelect id="sort-filter" v-model="filters.sort" aria-label="排序"
            ><ElOption label="出发日期 · 从新到旧" value="start_date_desc" /><ElOption
              label="最近更新"
              value="updated_at_desc"
          /></ElSelect>
        </div>
      </form>
    </ElCard>
    <ElAlert
      v-if="feedback.length"
      :type="feedbackType"
      :title="feedback.join(' ')"
      show-icon
      @close="feedback = []"
    />
    <ElAlert v-if="actionFailure" type="error" :title="actionFailure" :closable="false" show-icon />
    <div class="list-toolbar">
      <span>阶段按各旅行的时区计算，归档不改变阶段。</span
      ><IconAction icon="refresh" label="刷新旅行列表" :loading="loading" @click="reload" />
    </div>
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
            <div class="trip-card-title">
              <h3>
                <RouterLink
                  :to="{ name: 'trip-detail', params: { tripId: trip.id } }"
                  class="trip-link"
                  >{{ trip.name }}</RouterLink
                >
              </h3>
              <ElTag v-if="trip.archived_at" type="info" size="small">已归档</ElTag>
            </div>
            <p class="trip-destination">{{ trip.destination || '目的地待定' }}</p>
            <p class="trip-dates">{{ trip.start_date }} 至 {{ trip.end_date }}</p>
            <p class="trip-timezone">{{ trip.timezone }}</p>
            <p v-if="trip.notes" class="trip-notes">{{ trip.notes }}</p>
            <div class="trip-budget">
              <span>总预算</span
              ><strong>{{
                trip.budget_amount === null
                  ? '未设置'
                  : `${trip.currency_code} ${trip.budget_amount}`
              }}</strong>
            </div>
            <div class="trip-actions tf-actions">
              <RouterLink :to="{ name: 'trip-detail', params: { tripId: trip.id } }"
                ><ElButton size="small" type="primary" plain :disabled="!!busy"
                  >查看详情</ElButton
                ></RouterLink
              >
              <IconAction
                icon="edit"
                :label="`编辑旅行：${trip.name}`"
                :disabled="!!busy"
                @click="editor?.open(trip)"
              />
              <ElButton
                size="small"
                :disabled="!!busy && busy !== trip.id"
                :loading="busy === trip.id"
                @click="mutate(trip, 'archive')"
                >{{ trip.archived_at ? '取消归档' : '归档' }}</ElButton
              >
              <IconAction
                icon="trash"
                :label="`移入回收站：${trip.name}`"
                type="danger"
                plain
                :disabled="!!busy"
                @click="confirmTrash(trip)"
              />
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
.heading-actions .el-button {
  margin-left: 0;
}
.trip-filters {
  display: grid;
  grid-template-columns: minmax(260px, 2fr) minmax(130px, 1fr) minmax(130px, 1fr) minmax(
      180px,
      1fr
    );
  gap: 16px;
}
.trip-filters label {
  display: block;
  font-size: 12px;
  color: var(--tf-text-2);
  margin-bottom: 8px;
}
.search-input {
  display: flex;
  gap: 8px;
}
.list-toolbar {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: center;
  font-size: 12px;
  color: var(--tf-text-3);
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
  grid-template-columns: repeat(auto-fill, minmax(330px, 1fr));
  gap: 16px;
}
.trip-card {
  transition:
    transform var(--tf-duration) var(--tf-ease),
    box-shadow var(--tf-duration) var(--tf-ease);
}
.trip-card:hover {
  transform: translateY(-2px);
  box-shadow: var(--tf-shadow-2);
}
.trip-card :deep(.el-card__body) {
  display: flex;
  flex-direction: column;
  height: 100%;
  box-sizing: border-box;
}
.trip-card-title {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
}
.trip-card h3 {
  margin: 0;
  font-size: 18px;
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.trip-link {
  color: var(--tf-text-1);
  text-decoration: none;
}
.trip-link:hover {
  color: var(--tf-accent);
}
.trip-destination {
  margin: 8px 0 16px;
  color: var(--tf-text-2);
  overflow-wrap: anywhere;
}
.trip-dates {
  margin: 0 0 5px;
  font-size: 14px;
}
.trip-timezone {
  margin: 0 0 14px;
  font-size: 12px;
  color: var(--tf-text-3);
}
.trip-notes {
  margin: 0 0 16px;
  color: var(--tf-text-3);
  font-size: 13px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  white-space: pre-wrap;
}
.trip-budget {
  display: flex;
  gap: 16px;
  justify-content: space-between;
  border-top: 1px solid var(--tf-line-soft);
  padding-top: 14px;
  margin-top: auto;
  font-size: 13px;
}
.trip-budget > span {
  color: var(--tf-text-3);
}
.trip-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 18px;
}
.trip-actions .el-button {
  margin-left: 0;
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
@media (max-width: 1000px) {
  .trip-filters {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 600px) {
  .page-heading {
    align-items: flex-start;
    flex-direction: column;
  }
  .trip-filters,
  .trip-grid {
    grid-template-columns: 1fr;
  }
}
</style>
