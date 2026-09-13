<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCard,
  ElCheckbox,
  ElEmpty,
  ElMessageBox,
  ElProgress,
  ElRadioButton,
  ElRadioGroup,
  ElSkeleton,
  ElTag,
} from 'element-plus'
import { computed, onMounted, ref, shallowRef, watch } from 'vue'

import TodoDialog from '@/desktop/components/TodoDialog.vue'
import { ApiError } from '@/shared/api/auth'
import {
  deleteTodo,
  listAllTodos,
  todoStateLabels,
  updateTodo,
  type Todo,
  type TodoListItem,
  type TodoState,
} from '@/shared/api/todos'
import {
  actionError,
  createWriteIntent,
  writeWarnings,
  type WriteOutcome,
} from '@/shared/api/writes'
import { useTripContext } from '@/shared/travel/tripContext'
import { dayTitle } from '@/shared/travel/tripDays'

const context = useTripContext()
const items = shallowRef<TodoListItem[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const busy = ref<string | null>(null)
const actionFailure = ref<string | null>(null)
const notice = ref<string[]>([])
const noticeType = ref<'success' | 'warning'>('success')
const state = ref<TodoState>('all')
const dialog = ref<InstanceType<typeof TodoDialog>>()
const intents = new Map<string, ReturnType<typeof createWriteIntent>>()
let generation = 0

const states = Object.keys(todoStateLabels) as TodoState[]
const progress = computed(() => {
  const total = items.value.length
  const done = items.value.filter((i) => i.completed).length
  const overdue = items.value.filter((i) => i.is_overdue).length
  return { total, done, overdue, percent: total ? Math.round((done / total) * 100) : 0 }
})

async function reload() {
  const request = ++generation
  loading.value = true
  error.value = null
  try {
    const loaded = await listAllTodos(context.tripId, state.value)
    if (request === generation) items.value = loaded
  } catch (cause) {
    if (request === generation) error.value = actionError(cause, '无法加载待办，请检查网络后重试')
  } finally {
    if (request === generation) loading.value = false
  }
}

function intentFor(slot: string) {
  const existing = intents.get(slot) ?? createWriteIntent()
  intents.set(slot, existing)
  return existing
}

async function toggle(item: TodoListItem, completed: boolean) {
  if (busy.value || item.completed === completed) return
  busy.value = item.id
  actionFailure.value = null
  const intent = intentFor(`complete:${item.id}`)
  try {
    const outcome = await updateTodo(
      context.tripId,
      item.id,
      item.version,
      { completed },
      intent.key({ id: item.id, version: item.version, completed }),
    )
    intent.reset()
    const warnings = writeWarnings(outcome.result)
    if (warnings.length) {
      noticeType.value = 'warning'
      notice.value = warnings
    }
    // 逾期标识由列表上下文计算，本地按同一规则更新，避免整表刷新
    if (outcome.resource) {
      const next = outcome.resource
      items.value = items.value.map((i) =>
        i.id === item.id
          ? {
              ...next,
              is_overdue: !next.completed && !!next.due_on && next.due_on < context.today.value,
            }
          : i,
      )
      if (state.value !== 'all') await reload()
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

async function remove(item: TodoListItem) {
  if (busy.value) return
  try {
    await ElMessageBox.confirm(`删除“${item.title}”？`, '删除这条待办', {
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
    await deleteTodo(
      context.tripId,
      item.id,
      item.version,
      intent.key({ delete: item.id, version: item.version }),
    )
    intent.reset()
    noticeType.value = 'success'
    notice.value = ['待办已删除。']
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

async function saved(outcome: WriteOutcome<Todo>) {
  const warnings = writeWarnings(outcome.result)
  noticeType.value = warnings.length ? 'warning' : 'success'
  notice.value = ['待办已保存。', ...warnings]
  actionFailure.value = null
  await reload()
}

function dueLabel(item: TodoListItem) {
  if (!item.due_on) return '不限期'
  return `${dayTitle(item.due_on)}${item.due_on.slice(0, 4) !== context.today.value.slice(0, 4) ? `（${item.due_on.slice(0, 4)}）` : ''}`
}

watch(state, reload)
onMounted(reload)
</script>

<template>
  <div class="todos-tab">
    <ElCard shadow="never">
      <div class="progress-row">
        <div class="progress-text">
          <strong>已完成 {{ progress.done }} / {{ progress.total }}</strong>
          <span v-if="progress.overdue" class="overdue-count">{{ progress.overdue }} 项已逾期</span>
          <span v-else>逾期按旅行时区 {{ context.trip.value?.timezone }} 的今天判定</span>
        </div>
        <ElProgress :percentage="progress.percent" :stroke-width="10" class="progress-bar" />
      </div>
    </ElCard>
    <div class="tab-toolbar">
      <ElRadioGroup v-model="state" size="small" aria-label="筛选待办">
        <ElRadioButton v-for="s in states" :key="s" :value="s">{{
          todoStateLabels[s]
        }}</ElRadioButton>
      </ElRadioGroup>
      <div class="tab-actions">
        <ElButton size="small" :loading="loading" @click="reload">刷新</ElButton>
        <ElButton size="small" type="primary" @click="dialog?.open()">新建待办</ElButton>
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
    <ElSkeleton v-if="loading && !items.length" :rows="6" animated class="tab-skeleton" />
    <ElCard v-else-if="error" shadow="never">
      <ElAlert :title="error" type="error" :closable="false" show-icon />
      <ElButton class="retry-button" @click="reload">重新加载</ElButton>
    </ElCard>
    <ElCard v-else-if="!items.length" shadow="never">
      <ElEmpty
        :description="
          state === 'all' ? '还没有待办，记下出发前要做的事' : `没有${todoStateLabels[state]}的待办`
        "
      >
        <ElButton v-if="state === 'all'" type="primary" @click="dialog?.open()">新建待办</ElButton>
        <ElButton v-else @click="state = 'all'">查看全部</ElButton>
      </ElEmpty>
    </ElCard>
    <ul v-else class="todo-list tf-surface">
      <li
        v-for="item in items"
        :key="item.id"
        class="todo"
        :class="{ 'todo--done': item.completed, 'todo--overdue': item.is_overdue }"
      >
        <ElCheckbox
          :model-value="item.completed"
          :disabled="!!busy && busy !== item.id"
          :aria-label="`${item.completed ? '恢复未完成' : '标记完成'}：${item.title}`"
          class="todo-check"
          @change="toggle(item, $event as boolean)"
        />
        <div class="todo-main">
          <div class="todo-title">
            <strong>{{ item.title }}</strong>
            <ElTag v-if="item.is_overdue" type="danger" size="small">逾期</ElTag>
          </div>
          <p class="todo-meta">
            <span :class="{ 'todo-due--overdue': item.is_overdue }">截止 {{ dueLabel(item) }}</span>
            <span v-if="item.completed_at"
              >已完成于 {{ new Date(item.completed_at).toLocaleDateString('zh-CN') }}</span
            >
          </p>
          <p v-if="item.notes" class="todo-notes">{{ item.notes }}</p>
        </div>
        <div class="todo-actions">
          <ElButton size="small" text :disabled="!!busy" @click="dialog?.open(item)">编辑</ElButton>
          <ElButton
            size="small"
            text
            type="danger"
            :disabled="!!busy"
            :loading="busy === item.id"
            @click="remove(item)"
            >删除</ElButton
          >
        </div>
      </li>
    </ul>
    <TodoDialog ref="dialog" @saved="saved" />
  </div>
</template>

<style scoped>
.todos-tab {
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
.overdue-count {
  color: var(--tf-danger) !important;
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
.todo-list {
  list-style: none;
  margin: 0;
  padding: 4px 16px;
}
.todo {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--tf-line-soft);
}
.todo:last-child {
  border-bottom: 0;
}
.todo--done .todo-title strong {
  color: var(--tf-text-3);
  text-decoration: line-through;
}
.todo--overdue {
  background: var(--tf-danger-soft);
  margin: 0 -16px;
  padding-left: 16px;
  padding-right: 16px;
}
.todo-check {
  margin-top: 2px;
}
.todo-main {
  flex: 1;
  min-width: 0;
}
.todo-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.todo-title strong {
  overflow-wrap: anywhere;
}
.todo-meta {
  margin: 4px 0 0;
  display: flex;
  gap: 14px;
  font-size: 12px;
  color: var(--tf-text-3);
}
.todo-due--overdue {
  color: var(--tf-danger);
  font-weight: 600;
}
.todo-notes {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--tf-text-2);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.todo-actions {
  display: flex;
  flex-shrink: 0;
}
.todo-actions .el-button {
  margin-left: 0;
}
</style>
