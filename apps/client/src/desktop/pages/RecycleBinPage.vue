<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElCard,
  ElCheckbox,
  ElDialog,
  ElEmpty,
  ElForm,
  ElFormItem,
  ElInput,
  ElSkeleton,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'

import IconAction from '@/desktop/components/IconAction.vue'
import type { TrashedTrip } from '@/shared/api/trips'
import DeletionJobStatus from '@/desktop/components/DeletionJobStatus.vue'
import {
  canRestoreTrip,
  formatTripTime,
  retentionLabel,
  useRecycleBin,
} from '@/shared/travel/useRecycleBin'

const recycle = useRecycleBin()
const {
  items,
  cursor,
  loading,
  loadingMore,
  error,
  moreError,
  now,
  busy,
  actionError,
  feedback,
  selected,
  purgeOpened,
  loadingSelected,
  selectionError,
  purging,
  confirmed,
  password,
  purgeError,
  retryJob,
} = recycle
const { tasks, loading: loadingTasks } = recycle.deletionJobs

function row(value: unknown): TrashedTrip {
  return value as TrashedTrip
}
function closePurge(done?: () => void) {
  if (!purging.value) {
    recycle.closePurge()
    done?.()
  }
}
</script>

<template>
  <div class="recycle-page">
    <header class="recycle-heading">
      <RouterLink class="page-back" :to="{ name: 'account' }">
        <span aria-hidden="true">←</span>
        <span>返回我的</span>
      </RouterLink>
      <div class="recycle-title-row">
        <div>
          <h1>旅行回收站</h1>
          <p>整趟旅行及关联内容可在恢复截止时间前一并恢复。</p>
        </div>
        <IconAction
          icon="refresh"
          label="刷新回收站"
          :loading="loading"
          :disabled="purging || !!busy"
          @click="recycle.reload"
        />
      </div>
    </header>
    <ElAlert
      title="旅行进入回收站后保留 30 天。永久清理一经请求，便无法恢复。"
      type="info"
      :closable="false"
      show-icon
    />
    <ElAlert v-if="feedback" :title="feedback" type="success" show-icon @close="feedback = null" />
    <ElAlert v-if="actionError" :title="actionError" type="error" :closable="false" show-icon />
    <ElCard v-if="tasks.length" shadow="never">
      <template #header
        ><div class="recycle-title-row">
          <span>永久清理进度</span
          ><IconAction
            icon="refresh"
            label="刷新清理进度"
            :loading="loadingTasks"
            @click="recycle.deletionJobs.refresh"
          /></div
      ></template>
      <ul class="cleanup-tasks">
        <li v-for="task in tasks" :key="task.tripId">
          <h2>{{ items.find((trip) => trip.id === task.tripId)?.name ?? '旅行清理任务' }}</h2>
          <DeletionJobStatus v-if="task.job" :job="task.job" />
          <p v-else>申请已受理，正在查询处理状态。</p>
          <ElAlert v-if="task.error" :title="task.error" type="error" :closable="false" />
          <div class="tf-actions">
            <ElButton
              v-if="task.job?.retryable"
              type="danger"
              plain
              :disabled="purging || !!busy"
              @click="recycle.openRetry(task.tripId, task.job)"
              >验证身份并重试清理</ElButton
            >
            <ElButton
              v-if="task.job?.status === 'completed'"
              @click="recycle.deletionJobs.dismiss(task.tripId)"
              >收起已完成任务</ElButton
            >
          </div>
        </li>
      </ul>
    </ElCard>
    <ElCard shadow="never">
      <ElSkeleton v-if="loading" :rows="6" animated />
      <div v-else-if="error">
        <ElAlert :title="error" type="error" :closable="false" show-icon /><ElButton
          class="recycle-retry"
          @click="recycle.reload"
          >重新加载</ElButton
        >
      </div>
      <ElEmpty v-else-if="!items.length" description="回收站是空的"
        ><RouterLink :to="{ name: 'trips' }"><ElButton>返回旅行列表</ElButton></RouterLink></ElEmpty
      >
      <template v-else>
        <ul class="recycle-mobile-list" aria-label="回收站旅行">
          <li v-for="trip in items" :key="trip.id">
            <h2>{{ trip.name }}</h2>
            <p>
              {{ trip.destination || '目的地待定' }} · {{ trip.start_date }} 至 {{ trip.end_date }}
            </p>
            <dl>
              <div>
                <dt>删除时间</dt>
                <dd>{{ formatTripTime(trip.deleted_at) }}</dd>
              </div>
              <div>
                <dt>恢复截止</dt>
                <dd>{{ formatTripTime(trip.purge_after_at) }}</dd>
              </div>
            </dl>
            <p class="retention-label">{{ retentionLabel(trip, now) }}</p>
            <div class="recycle-actions tf-actions">
              <ElButton
                v-if="!trip.purge_requested_at"
                :loading="busy === trip.id"
                :disabled="!canRestoreTrip(trip, now) || purging || (!!busy && busy !== trip.id)"
                @click="recycle.restore(trip)"
                >恢复旅行</ElButton
              >
              <ElButton
                v-if="!trip.purge_requested_at"
                type="danger"
                plain
                :disabled="purging || !!busy"
                @click="recycle.openPurge(trip)"
                >永久清理</ElButton
              >
            </div>
          </li>
        </ul>
        <ElTable :data="items" row-key="id" class="recycle-table">
          <ElTableColumn label="旅行" min-width="230"
            ><template #default="{ row: value }"
              ><div class="recycle-trip">
                <strong>{{ row(value).name }}</strong
                ><span>{{ row(value).destination || '目的地待定' }}</span
                ><span>{{ row(value).start_date }} 至 {{ row(value).end_date }}</span
                ><ElTag v-if="row(value).archived_at" size="small" type="info">已归档</ElTag>
              </div></template
            ></ElTableColumn
          >
          <ElTableColumn label="删除时间" min-width="175"
            ><template #default="{ row: value }">{{
              formatTripTime(row(value).deleted_at)
            }}</template></ElTableColumn
          >
          <ElTableColumn label="恢复截止时间" min-width="180"
            ><template #default="{ row: value }">{{
              formatTripTime(row(value).purge_after_at)
            }}</template></ElTableColumn
          >
          <ElTableColumn label="保留状态" min-width="205"
            ><template #default="{ row: value }"
              ><ElTag v-if="row(value).purge_requested_at" type="warning">已请求永久清理</ElTag>
              <p class="retention-label">{{ retentionLabel(row(value), now) }}</p>
              <span v-if="row(value).purge_requested_at" class="recycle-muted"
                >等待清理完成</span
              ></template
            ></ElTableColumn
          >
          <ElTableColumn label="操作" width="190" fixed="right"
            ><template #default="{ row: value }"
              ><div class="recycle-actions tf-actions">
                <ElButton
                  v-if="!row(value).purge_requested_at"
                  size="small"
                  :loading="busy === row(value).id"
                  :disabled="
                    !canRestoreTrip(row(value), now) ||
                    purging ||
                    (!!busy && busy !== row(value).id)
                  "
                  :aria-label="`恢复旅行${row(value).name}`"
                  @click="recycle.restore(row(value))"
                  >恢复</ElButton
                >
                <IconAction
                  icon="trash"
                  :label="`永久清理旅行：${row(value).name}`"
                  type="danger"
                  plain
                  :disabled="!!row(value).purge_requested_at || purging || !!busy"
                  @click="recycle.openPurge(row(value))"
                /></div></template
          ></ElTableColumn>
        </ElTable>
      </template>
    </ElCard>
    <div v-if="items.length" class="recycle-pagination">
      <span>已加载 {{ items.length }} 趟旅行</span
      ><ElAlert v-if="moreError" :title="moreError" type="error" :closable="false" /><ElButton
        v-if="cursor"
        :loading="loadingMore"
        @click="recycle.loadMore"
        >{{ moreError ? '重试加载下一页' : '加载更多' }}</ElButton
      ><span v-else>已显示全部回收站旅行</span>
    </div>

    <ElDialog
      :model-value="purgeOpened"
      :title="retryJob ? '重试永久清理旅行' : '请求永久清理旅行'"
      width="min(560px, calc(100vw - 32px))"
      :close-on-click-modal="false"
      :close-on-press-escape="!purging"
      :before-close="closePurge"
      destroy-on-close
    >
      <ElSkeleton v-if="loadingSelected" :rows="4" animated />
      <template v-else-if="selected">
        <ElAlert title="永久清理无法撤销" type="error" :closable="false" show-icon />
        <p class="purge-scope">
          此操作将永久清理“<strong>{{ selected.name }}</strong
          >”以及它的全部行程、账目、行李、待办、预订、资料和照片。请求提交后便无法恢复。
        </p>
        <p class="recycle-muted">
          删除时间：{{ formatTripTime(selected.deleted_at) }}<br />恢复截止时间：{{
            formatTripTime(selected.purge_after_at)
          }}
        </p>
        <ElAlert
          v-if="selected.purge_requested_at && !retryJob"
          title="这趟旅行已请求永久清理，正在等待完成。"
          type="warning"
          :closable="false"
        />
        <ElAlert
          v-if="selectionError"
          :title="selectionError"
          type="error"
          :closable="false"
          class="purge-error"
        />
        <ElButton v-if="selectionError" @click="recycle.loadSelected">重新加载最新信息</ElButton>
        <ElAlert
          v-if="purgeError"
          :title="purgeError"
          type="error"
          :closable="false"
          class="purge-error"
        />
        <ElForm
          :disabled="purging || !!selectionError || (!!selected.purge_requested_at && !retryJob)"
          label-position="top"
          @submit.prevent="recycle.purge"
        >
          <ElFormItem
            ><ElCheckbox v-model="confirmed" class="purge-confirm"
              >我确认永久清理整趟旅行及全部关联数据，并理解提交后无法恢复。</ElCheckbox
            ></ElFormItem
          >
          <ElFormItem label="当前账号密码" required
            ><ElInput
              v-model="password"
              type="password"
              show-password
              autocomplete="current-password"
              placeholder="验证当前密码后提交请求"
          /></ElFormItem>
          <button type="submit" class="visually-hidden" tabindex="-1" aria-hidden="true">
            验证密码并请求永久清理
          </button>
        </ElForm>
      </template>
      <template #footer
        ><ElButton :disabled="purging" @click="closePurge()">取消</ElButton
        ><ElButton
          type="danger"
          :loading="purging"
          :disabled="
            loadingSelected ||
            !!selectionError ||
            !confirmed ||
            !password ||
            (!!selected?.purge_requested_at && !retryJob)
          "
          @click="recycle.purge"
          >{{ retryJob ? '验证密码并重试清理' : '验证密码并请求永久清理' }}</ElButton
        ></template
      >
    </ElDialog>
  </div>
</template>

<style scoped>
.recycle-page {
  max-width: 1240px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.cleanup-tasks {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.cleanup-tasks li {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}
.cleanup-tasks li + li {
  border-top: 1px solid var(--tf-line-soft);
  padding-top: 20px;
}
.cleanup-tasks h2 {
  margin: 0;
  font-size: 17px;
  overflow-wrap: anywhere;
}
.recycle-heading {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 24px;
  padding: 8px 0;
}
.page-back {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--tf-text-2);
  font-size: 13px;
  font-weight: 600;
  text-decoration: none;
}
.page-back:hover {
  color: var(--tf-accent);
}
.page-back:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 3px;
}
.page-back span:first-child {
  font-size: 18px;
  line-height: 1;
}
.recycle-heading h1 {
  margin: 0;
  font-size: 28px;
}
.recycle-heading p {
  margin: 8px 0 0;
  color: var(--tf-text-3);
}
.recycle-title-row {
  display: flex;
  width: 100%;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.recycle-mobile-list {
  display: none;
}
@media (max-width: 767px) {
  .recycle-table {
    display: none;
  }
  .recycle-mobile-list {
    display: block;
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .recycle-mobile-list li + li {
    margin-top: 20px;
    padding-top: 20px;
    border-top: 1px solid var(--tf-line-soft);
  }
  .recycle-mobile-list h2 {
    margin: 0;
    font-size: 18px;
    overflow-wrap: anywhere;
  }
  .recycle-mobile-list p {
    line-height: 1.7;
    font-size: 13px;
    color: var(--tf-text-2);
    overflow-wrap: anywhere;
  }
  .recycle-mobile-list dl {
    font-size: 12px;
    line-height: 1.8;
    color: var(--tf-text-3);
  }
  .recycle-mobile-list dl div {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
  }
  .recycle-mobile-list dd {
    margin: 0;
  }
  .recycle-mobile-list .recycle-actions {
    flex-wrap: wrap;
    gap: 8px;
  }
}
.recycle-trip {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  padding: 8px 0;
}
.recycle-trip strong {
  color: var(--tf-text-1);
  font-size: 15px;
  overflow-wrap: anywhere;
}
.recycle-trip span,
.recycle-muted {
  color: var(--tf-text-3);
  font-size: 12px;
  line-height: 1.8;
}
.recycle-actions {
  display: flex;
}
.retention-label {
  margin: 6px 0;
  font-size: 12px;
}
.recycle-retry {
  margin-top: 16px;
}
.recycle-pagination {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  color: var(--tf-text-3);
  font-size: 12px;
}
.purge-scope {
  line-height: 1.9;
  margin: 18px 0 10px;
  overflow-wrap: anywhere;
}
.purge-error {
  margin: 16px 0;
}
.purge-confirm {
  height: auto;
  align-items: flex-start;
  margin-top: 16px;
}
.purge-confirm :deep(.el-checkbox__input) {
  margin-top: 4px;
}
.purge-confirm :deep(.el-checkbox__label) {
  white-space: normal;
  line-height: 1.7;
}
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}
</style>
