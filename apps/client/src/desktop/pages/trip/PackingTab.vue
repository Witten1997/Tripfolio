<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCard,
  ElEmpty,
  ElMessageBox,
  ElOption,
  ElProgress,
  ElSelect,
  ElSkeleton,
  ElTag,
} from 'element-plus'
import { computed, onMounted, reactive, ref, shallowRef } from 'vue'

import PackingItemDialog from '@/desktop/components/PackingItemDialog.vue'
import PackingLibraryDialog from '@/desktop/components/PackingLibraryDialog.vue'
import { ApiError } from '@/shared/api/auth'
import {
  deletePackingItem,
  listAllPackingItems,
  packingCategoryLabels,
  packingCategoryOrder,
  packingStatusLabels,
  packingStatusOrder,
  updatePackingItem,
  type PackingBatchResult,
  type PackingCategory,
  type PackingItem,
  type PackingStatus,
} from '@/shared/api/packing'
import {
  actionError,
  createWriteIntent,
  writeWarnings,
  type WriteOutcome,
} from '@/shared/api/writes'
import { useTripContext } from '@/shared/travel/tripContext'

const context = useTripContext()
const items = shallowRef<PackingItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const busy = ref<string | null>(null)
const actionFailure = ref<string | null>(null)
const notice = ref<string[]>([])
const noticeType = ref<'success' | 'warning'>('success')
const filters = reactive({ category: '' as '' | PackingCategory, status: '' as '' | PackingStatus })
const dialog = ref<InstanceType<typeof PackingItemDialog>>()
const library = ref<InstanceType<typeof PackingLibraryDialog>>()
const intents = new Map<string, ReturnType<typeof createWriteIntent>>()
let generation = 0

const progress = computed(() => {
  const total = items.value.length
  const ready = items.value.filter((i) => i.status !== 'pending').length
  const packed = items.value.filter((i) => i.status === 'packed').length
  return { total, ready, packed, percent: total ? Math.round((packed / total) * 100) : 0 }
})

const groups = computed(() =>
  packingCategoryOrder
    .filter((category) => !filters.category || filters.category === category)
    .map((category) => ({
      category,
      label: packingCategoryLabels[category],
      items: items.value.filter(
        (i) => i.category === category && (!filters.status || i.status === filters.status),
      ),
    }))
    .filter((g) => g.items.length || (!filters.status && !filters.category)),
)
const isFiltered = computed(() => !!filters.category || !!filters.status)

async function reload() {
  const request = ++generation
  loading.value = true
  error.value = null
  try {
    const loaded = await listAllPackingItems(context.tripId)
    if (request === generation) items.value = loaded
  } catch (cause) {
    if (request === generation)
      error.value = actionError(cause, '无法加载行李清单，请检查网络后重试')
  } finally {
    if (request === generation) loading.value = false
  }
}

function intentFor(slot: string) {
  const existing = intents.get(slot) ?? createWriteIntent()
  intents.set(slot, existing)
  return existing
}

async function setStatus(item: PackingItem, status: PackingStatus) {
  if (busy.value || item.status === status) return
  busy.value = item.id
  actionFailure.value = null
  const intent = intentFor(`status:${item.id}`)
  try {
    const outcome = await updatePackingItem(
      context.tripId,
      item.id,
      item.version,
      { status },
      intent.key({ id: item.id, version: item.version, status }),
    )
    intent.reset()
    const warnings = writeWarnings(outcome.result)
    if (warnings.length) {
      noticeType.value = 'warning'
      notice.value = warnings
    }
    if (outcome.resource) {
      items.value = items.value.map((i) => (i.id === item.id ? outcome.resource! : i))
    } else await reload()
  } catch (cause) {
    actionFailure.value = actionError(cause, '网络连接中断，结果尚未确认。可重试或刷新后确认。')
    if (
      cause instanceof ApiError &&
      ['VERSION_CONFLICT', 'RESOURCE_GONE'].includes(cause.code ?? '')
    )
      await reload()
  } finally {
    busy.value = null
  }
}

async function remove(item: PackingItem) {
  if (busy.value) return
  try {
    await ElMessageBox.confirm(`删除“${item.name}”后可重新添加同名物品。`, '删除这件物品？', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '保留',
    })
  } catch {
    return
  }
  busy.value = item.id
  actionFailure.value = null
  const intent = intentFor(`delete:${item.id}`)
  try {
    await deletePackingItem(
      context.tripId,
      item.id,
      item.version,
      intent.key({ delete: item.id, version: item.version }),
    )
    intent.reset()
    noticeType.value = 'success'
    notice.value = ['物品已删除。']
    await reload()
  } catch (cause) {
    actionFailure.value = actionError(cause, '网络连接中断，结果尚未确认。可重试或刷新后确认。')
    if (
      cause instanceof ApiError &&
      ['VERSION_CONFLICT', 'RESOURCE_GONE'].includes(cause.code ?? '')
    )
      await reload()
  } finally {
    busy.value = null
  }
}

async function saved(outcome: WriteOutcome<PackingItem>) {
  const warnings = writeWarnings(outcome.result)
  noticeType.value = warnings.length ? 'warning' : 'success'
  notice.value = ['物品已保存。', ...warnings]
  actionFailure.value = null
  await reload()
}

async function added(result: PackingBatchResult) {
  const created = result.created_ids.length
  const skipped = result.skipped.length
  noticeType.value = skipped ? 'warning' : 'success'
  notice.value = [
    created ? `已加入 ${created} 件物品。` : '没有新加入的物品。',
    ...(skipped ? [`已跳过 ${skipped} 件同名物品。`] : []),
    ...writeWarnings(result),
  ]
  await reload()
}

function nextStatus(status: PackingStatus): PackingStatus | null {
  const index = packingStatusOrder.indexOf(status)
  return packingStatusOrder[index + 1] ?? null
}
function prevStatus(status: PackingStatus): PackingStatus | null {
  const index = packingStatusOrder.indexOf(status)
  return index > 0 ? packingStatusOrder[index - 1]! : null
}
const statusType = (status: PackingStatus) =>
  status === 'packed' ? 'success' : status === 'ready' ? 'primary' : 'info'

onMounted(reload)
</script>

<template>
  <div class="packing-tab">
    <ElCard shadow="never" class="progress-card">
      <div class="progress-row">
        <div class="progress-text">
          <strong>已装包 {{ progress.packed }} / {{ progress.total }}</strong>
          <span>已备齐（含已装包）{{ progress.ready }} 件 · 按条目计数</span>
        </div>
        <ElProgress :percentage="progress.percent" :stroke-width="10" class="progress-bar" />
      </div>
    </ElCard>
    <div class="tab-toolbar">
      <div class="tab-filters">
        <ElSelect
          v-model="filters.category"
          aria-label="分类"
          placeholder="全部分类"
          clearable
          size="small"
        >
          <ElOption
            v-for="c in packingCategoryOrder"
            :key="c"
            :label="packingCategoryLabels[c]"
            :value="c"
          />
        </ElSelect>
        <ElSelect
          v-model="filters.status"
          aria-label="状态"
          placeholder="全部状态"
          clearable
          size="small"
        >
          <ElOption
            v-for="s in packingStatusOrder"
            :key="s"
            :label="packingStatusLabels[s]"
            :value="s"
          />
        </ElSelect>
      </div>
      <div class="tab-actions">
        <ElButton size="small" :loading="loading" @click="reload">刷新</ElButton>
        <ElButton size="small" @click="library?.open(items)">从物品库添加</ElButton>
        <ElButton size="small" type="primary" @click="dialog?.open()">自定义物品</ElButton>
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
    <ElSkeleton v-if="loading && !items.length" :rows="8" animated class="tab-skeleton" />
    <ElCard v-else-if="error" shadow="never">
      <ElAlert :title="error" type="error" :closable="false" show-icon />
      <ElButton class="retry-button" @click="reload">重新加载</ElButton>
    </ElCard>
    <ElCard v-else-if="!items.length" shadow="never">
      <ElEmpty description="清单还是空的，从物品库挑选或自定义添加">
        <ElButton type="primary" @click="library?.open(items)">从物品库添加</ElButton>
      </ElEmpty>
    </ElCard>
    <ElCard v-else-if="!groups.length" shadow="never">
      <ElEmpty description="没有符合筛选的物品">
        <ElButton @click="Object.assign(filters, { category: '', status: '' })">清除筛选</ElButton>
      </ElEmpty>
    </ElCard>
    <div v-else class="group-list">
      <section v-for="group in groups" :key="group.category" class="group tf-surface">
        <header class="group-header">
          <h2>{{ group.label }}</h2>
          <span class="group-count">{{ group.items.length }} 件</span>
          <ElButton
            size="small"
            text
            :disabled="!!busy"
            @click="dialog?.open(undefined, group.category)"
            >添加</ElButton
          >
        </header>
        <ElEmpty v-if="!group.items.length" description="暂无物品" :image-size="40" />
        <ul v-else class="item-list">
          <li
            v-for="item in group.items"
            :key="item.id"
            class="item"
            :class="`item--${item.status}`"
          >
            <div class="item-main">
              <strong>{{ item.name }}</strong>
              <span v-if="item.quantity > 1" class="item-qty">×{{ item.quantity }}</span>
              <ElTag size="small" :type="statusType(item.status)" effect="plain">{{
                packingStatusLabels[item.status]
              }}</ElTag>
              <p v-if="item.notes" class="item-notes">{{ item.notes }}</p>
            </div>
            <div class="item-actions">
              <ElButton
                v-if="prevStatus(item.status)"
                size="small"
                text
                :disabled="!!busy && busy !== item.id"
                :loading="busy === item.id"
                @click="setStatus(item, prevStatus(item.status)!)"
                >改回{{ packingStatusLabels[prevStatus(item.status)!] }}</ElButton
              >
              <ElButton
                v-if="nextStatus(item.status)"
                size="small"
                :type="statusType(nextStatus(item.status)!)"
                plain
                :disabled="!!busy && busy !== item.id"
                :loading="busy === item.id"
                @click="setStatus(item, nextStatus(item.status)!)"
                >{{ packingStatusLabels[nextStatus(item.status)!] }}</ElButton
              >
              <ElButton size="small" text :disabled="!!busy" @click="dialog?.open(item)"
                >编辑</ElButton
              >
              <ElButton size="small" text type="danger" :disabled="!!busy" @click="remove(item)"
                >删除</ElButton
              >
            </div>
          </li>
        </ul>
      </section>
    </div>
    <PackingItemDialog ref="dialog" @saved="saved" />
    <PackingLibraryDialog ref="library" @added="added" />
  </div>
</template>

<style scoped>
.packing-tab {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.progress-row {
  display: flex;
  align-items: center;
  gap: 24px;
}
.progress-text {
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex-shrink: 0;
}
.progress-text span {
  font-size: 12px;
  color: var(--tf-text-3);
}
.progress-bar {
  flex: 1;
}
.tab-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}
.tab-filters {
  display: flex;
  gap: 8px;
}
.tab-filters .el-select {
  width: 140px;
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
.group-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(360px, 1fr));
  gap: 16px;
}
.group {
  padding: 14px 16px;
}
.group-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}
.group-header h2 {
  margin: 0;
  font-size: 16px;
}
.group-count {
  font-size: 12px;
  color: var(--tf-text-3);
  margin-right: auto;
}
.item-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
}
.item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 8px 0;
  border-top: 1px solid var(--tf-line-soft);
}
.item--packed .item-main strong {
  color: var(--tf-text-3);
  text-decoration: line-through;
}
.item-main {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  min-width: 0;
}
.item-main strong {
  overflow-wrap: anywhere;
}
.item-qty {
  font-size: 12px;
  color: var(--tf-text-3);
}
.item-notes {
  flex-basis: 100%;
  margin: 0;
  font-size: 12px;
  color: var(--tf-text-3);
  overflow-wrap: anywhere;
}
.item-actions {
  display: flex;
  flex-shrink: 0;
  flex-wrap: wrap;
  justify-content: flex-end;
}
.item-actions .el-button {
  margin-left: 0;
}
@media (max-width: 600px) {
  .group-list {
    grid-template-columns: 1fr;
  }
}
</style>
