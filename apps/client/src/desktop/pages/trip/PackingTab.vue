<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCard,
  ElCheckbox,
  ElEmpty,
  ElMessageBox,
  ElOption,
  ElProgress,
  ElSelect,
  ElSkeleton,
  ElTag,
} from 'element-plus'
import { computed, nextTick, onMounted, reactive, ref, shallowRef } from 'vue'

import IconAction from '@/desktop/components/IconAction.vue'
import PackingItemDialog from '@/desktop/components/PackingItemDialog.vue'
import PackingLibraryDialog from '@/desktop/components/PackingLibraryDialog.vue'
import { ApiError } from '@/shared/api/auth'
import {
  deletePackingItem,
  listAllPackingItems,
  normalizePackingStatus,
  packingCategoryLabels,
  packingCategoryOrder,
  packingStatusLabels,
  packingStatusOrder,
  updatePackingItem,
  type PackingBatchResult,
  type PackingCategory,
  type PackingItem,
  type PreparedPackingStatus,
} from '@/shared/api/packing'
import {
  actionError,
  createWriteIntent,
  writeWarnings,
  type WriteOutcome,
} from '@/shared/api/writes'
import { useTripContext } from '@/shared/travel/tripContext'
import { packingGroups, packingProgress } from '@/shared/travel/packingView'

const context = useTripContext()
const items = shallowRef<PackingItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const busy = ref<string | null>(null)
const actionFailure = ref<string | null>(null)
const notice = ref<string[]>([])
const noticeType = ref<'success' | 'warning'>('success')
const filters = reactive({
  category: '' as '' | PackingCategory,
  status: '' as '' | PreparedPackingStatus,
})
const dialog = ref<InstanceType<typeof PackingItemDialog>>()
const library = ref<InstanceType<typeof PackingLibraryDialog>>()
const intents = new Map<string, ReturnType<typeof createWriteIntent>>()
let generation = 0

const progress = computed(() => packingProgress(items.value))

const groups = computed(() => packingGroups(items.value, filters))

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

async function setStatus(item: PackingItem, status: PreparedPackingStatus) {
  if (busy.value || loading.value || normalizePackingStatus(item.status) === status) return
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

async function togglePrepared(item: PackingItem, checked: boolean, event?: Event) {
  const input = event?.target instanceof HTMLInputElement ? event.target : null
  const restoreFocus = input === document.activeElement
  const scope = input?.closest('.packing-tab')
  // 受控组件不更新 modelValue 时，原生 input 仍会先翻转。等待服务端确认期间
  // 恢复原生状态；失败时也不会留下“看似已准备、实际未保存”的勾选。
  if (input) input.checked = normalizePackingStatus(item.status) === 'ready'
  await setStatus(item, checked ? 'ready' : 'pending')
  await nextTick()
  // 原生 disabled 会使焦点落回 body；仅恢复这次交互，不打断用户已做的导航。
  if (
    restoreFocus &&
    scope?.isConnected &&
    (document.activeElement === document.body || document.activeElement === input)
  ) {
    const target = input?.isConnected
      ? input
      : (scope.querySelector<HTMLInputElement>('.tf-round-check input:not(:disabled)') ??
        scope.querySelector<HTMLInputElement>('.tf-filter-controls input[aria-label="状态"]'))
    if (target && !target.disabled) target.focus({ preventScroll: true })
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

onMounted(reload)
</script>

<template>
  <div class="packing-tab">
    <ElCard shadow="never" class="progress-card">
      <div class="progress-row">
        <div class="progress-text">
          <strong>已准备 {{ progress.ready }} / {{ progress.total }}</strong>
          <span>待准备 {{ progress.total - progress.ready }} 件 · 按条目计数</span>
        </div>
        <ElProgress
          :percentage="progress.percent"
          :stroke-width="10"
          class="progress-bar"
          aria-label="行李准备进度"
          :aria-valuetext="`已准备 ${progress.ready} / ${progress.total} 件`"
        />
      </div>
    </ElCard>
    <div class="tab-toolbar">
      <div class="tab-filters tf-filter-controls">
        <ElSelect v-model="filters.category" aria-label="分类" placeholder="全部分类" clearable>
          <ElOption
            v-for="c in packingCategoryOrder"
            :key="c"
            :label="packingCategoryLabels[c]"
            :value="c"
          />
        </ElSelect>
        <ElSelect v-model="filters.status" aria-label="状态" placeholder="全部状态" clearable>
          <ElOption
            v-for="s in packingStatusOrder"
            :key="s"
            :label="packingStatusLabels[s]"
            :value="s"
          />
        </ElSelect>
      </div>
      <div class="tab-actions tf-actions">
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
          <IconAction
            icon="plus"
            :label="`向${group.label}添加物品`"
            text
            :disabled="!!busy"
            @click="dialog?.open(undefined, group.category)"
          />
        </header>
        <ul class="item-list">
          <li
            v-for="item in group.items"
            :key="item.id"
            class="item"
            :class="{ 'item--prepared': normalizePackingStatus(item.status) === 'ready' }"
            :aria-busy="busy === item.id"
          >
            <ElCheckbox
              class="tf-round-check"
              :model-value="normalizePackingStatus(item.status) === 'ready'"
              :disabled="!!busy || loading"
              :aria-label="`已准备：${item.name}`"
              :label="`已准备：${item.name}`"
              @change="(checked: unknown, event?: Event) => togglePrepared(item, !!checked, event)"
            />
            <div class="item-main">
              <strong>{{ item.name }}</strong>
              <span v-if="item.quantity > 1" class="item-qty">×{{ item.quantity }}</span>
              <ElTag
                size="small"
                :type="normalizePackingStatus(item.status) === 'ready' ? 'success' : 'info'"
                effect="plain"
                >{{ packingStatusLabels[item.status] }}</ElTag
              >
              <p v-if="item.notes" class="item-notes">{{ item.notes }}</p>
            </div>
            <div class="item-actions tf-actions">
              <IconAction
                icon="edit"
                :label="`编辑物品：${item.name}`"
                text
                :disabled="!!busy"
                @click="dialog?.open(item)"
              />
              <IconAction
                icon="trash"
                :label="`删除物品：${item.name}`"
                text
                type="danger"
                :disabled="!!busy"
                @click="remove(item)"
              />
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
  flex-wrap: wrap;
}
.tab-filters .el-select {
  width: 140px;
}
.tab-actions {
  display: flex;
  align-items: center;
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
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 8px 0;
  border-top: 1px solid var(--tf-line-soft);
}
.item--prepared .item-main strong {
  color: var(--tf-text-3);
  text-decoration: line-through;
}
.item-main {
  display: flex;
  flex: 1 1 160px;
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
  max-width: 100%;
  margin-inline-start: auto;
}
.item-actions .el-button {
  margin-left: 0;
}
@media (max-width: 600px) {
  .group-list {
    grid-template-columns: 1fr;
  }
  .item {
    display: grid;
    grid-template-columns: var(--tf-control-size) minmax(0, 1fr);
  }
  .item-actions {
    grid-column: 2;
    justify-self: end;
  }
  .tab-actions {
    flex-wrap: wrap;
  }
  .progress-row {
    align-items: stretch;
    flex-direction: column;
    gap: 12px;
  }
}
</style>
