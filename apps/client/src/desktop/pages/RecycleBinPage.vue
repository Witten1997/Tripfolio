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
} = recycle

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
      <div>
        <h1>旅行回收站</h1>
        <p>整趟旅行及关联内容可在恢复截止时间前一并恢复。</p>
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
      <ElTable v-else :data="items" row-key="id" class="recycle-table">
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
                size="small"
                :loading="busy === row(value).id"
                :disabled="
                  !canRestoreTrip(row(value), now) || purging || (!!busy && busy !== row(value).id)
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
      title="请求永久清理旅行"
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
          v-if="selected.purge_requested_at"
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
          :disabled="purging || !!selectionError || !!selected.purge_requested_at"
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
            !!selected?.purge_requested_at
          "
          @click="recycle.purge"
          >验证密码并请求永久清理</ElButton
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
.recycle-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 8px 0;
}
.recycle-heading h1 {
  margin: 0;
  font-size: 28px;
}
.recycle-heading p {
  margin: 8px 0 0;
  color: var(--tf-text-3);
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
