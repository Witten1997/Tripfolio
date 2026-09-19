<script setup lang="ts">
import {
  ElAlert,
  ElButton,
  ElDialog,
  ElEmpty,
  ElInput,
  ElMessageBox,
  ElSkeleton,
  ElTag,
} from 'element-plus'

import IconAction from '@/desktop/components/IconAction.vue'
import { formatPercent, MAX_TRIP_MEMBERS, useTripMembers } from '@/shared/travel/useTripMembers'

const props = defineProps<{ tripId: string }>()
const emit = defineEmits<{ saved: [] }>()
const manager = useTripMembers(props.tripId)
const {
  opened,
  loading,
  saving,
  loadError,
  error,
  feedback,
  errors,
  rows,
  gap,
  dirty,
  invalidRows,
  canSave,
} = manager

async function requestClose(done?: () => void) {
  if (saving.value) return
  if (dirty.value) {
    try {
      await ElMessageBox.confirm('尚有未保存的成员改动，关闭后将放弃这些改动。', '关闭成员管理', {
        confirmButtonText: '放弃改动并关闭',
        cancelButtonText: '继续编辑',
        type: 'warning',
      })
    } catch {
      return
    }
  }
  manager.close()
  done?.()
}

async function save() {
  if (await manager.save()) emit('saved')
}

function rowError(index: number, field: 'name' | 'share_percent') {
  return invalidRows.value[`${index}.${field}`] ?? errors.value[`members[${index}].${field}`]
}

defineExpose({ open: manager.open })
</script>

<template>
  <ElDialog
    :model-value="opened"
    title="旅行成员"
    width="min(640px, calc(100vw - 32px))"
    :close-on-click-modal="false"
    :close-on-press-escape="!saving"
    :before-close="requestClose"
    destroy-on-close
  >
    <p class="members-intro">
      成员用于记账时选择付款人与分摊参与人。所有成员的分摊百分比之和必须等于
      100；「我」不能删除，被账目引用的成员也不能删除。
    </p>
    <ElAlert
      v-if="feedback"
      :title="feedback"
      type="success"
      show-icon
      class="members-alert"
      @close="feedback = null"
    />
    <ElAlert
      v-if="error"
      :title="error"
      type="error"
      :closable="false"
      show-icon
      class="members-alert"
    />
    <ElAlert
      v-if="loadError"
      :title="loadError"
      type="error"
      :closable="false"
      show-icon
      class="members-alert"
    >
      <ElButton size="small" @click="manager.load">重新加载</ElButton>
    </ElAlert>
    <ElSkeleton v-if="loading && !rows.length" :rows="3" animated />
    <template v-else>
      <div class="members-toolbar">
        <span class="members-sum" :class="{ 'members-sum--bad': gap !== 0 }" aria-live="polite">
          <template v-if="gap === null">百分比格式有误</template>
          <template v-else-if="gap === 0">合计 100%</template>
          <template v-else-if="gap > 0"
            >合计 {{ formatPercent(10000 - gap) }}%，还差 {{ formatPercent(gap) }}%</template
          >
          <template v-else
            >合计 {{ formatPercent(10000 - gap) }}%，超出 {{ formatPercent(-gap) }}%</template
          >
        </span>
        <div class="tf-actions">
          <ElButton size="small" :disabled="saving || !rows.length" @click="manager.equalize"
            >平均分配</ElButton
          >
          <ElButton
            size="small"
            type="primary"
            :disabled="saving || rows.length >= MAX_TRIP_MEMBERS"
            @click="manager.add"
            >添加成员</ElButton
          >
        </div>
      </div>
      <ElEmpty v-if="!rows.length" description="暂无成员" />
      <ol v-else class="members-list">
        <li v-for="(row, index) in rows" :key="row.id" class="member-row">
          <div class="member-order tf-actions">
            <IconAction
              icon="arrow-up"
              :label="`上移：${row.name || '未命名成员'}`"
              text
              :disabled="saving || index === 0"
              @click="manager.move(index, -1)"
            />
            <IconAction
              icon="arrow-down"
              :label="`下移：${row.name || '未命名成员'}`"
              text
              :disabled="saving || index === rows.length - 1"
              @click="manager.move(index, 1)"
            />
          </div>
          <div class="member-field">
            <ElInput
              v-model="row.name"
              maxlength="30"
              placeholder="成员名称"
              :aria-label="`成员名称 ${index + 1}`"
              :disabled="saving"
            />
            <span v-if="rowError(index, 'name')" class="member-error">{{
              rowError(index, 'name')
            }}</span>
          </div>
          <div class="member-field member-field--percent">
            <ElInput
              v-model="row.share_percent"
              inputmode="decimal"
              placeholder="0"
              :aria-label="`分摊百分比 ${index + 1}`"
              :disabled="saving"
            >
              <template #suffix>%</template>
            </ElInput>
            <span v-if="rowError(index, 'share_percent')" class="member-error">{{
              rowError(index, 'share_percent')
            }}</span>
          </div>
          <ElTag v-if="row.is_self" size="small" type="info">我</ElTag>
          <IconAction
            v-else
            icon="trash"
            :label="`删除成员：${row.name || '未命名成员'}`"
            text
            type="danger"
            :disabled="saving"
            @click="manager.remove(index)"
          />
        </li>
      </ol>
    </template>
    <template #footer>
      <ElButton :disabled="saving" @click="requestClose()">关闭</ElButton>
      <ElButton type="primary" :loading="saving" :disabled="!canSave" @click="save"
        >保存成员</ElButton
      >
    </template>
  </ElDialog>
</template>

<style scoped>
.members-intro {
  margin: 0 0 16px;
  color: var(--tf-text-3);
  line-height: 1.7;
}
.members-alert {
  margin-bottom: 16px;
}
.members-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 14px;
}
.members-toolbar .tf-actions {
  display: flex;
  gap: 8px;
}
.members-toolbar .el-button {
  margin-left: 0;
}
.members-sum {
  font-size: 13px;
  color: var(--tf-success);
}
.members-sum--bad {
  color: var(--tf-warning);
}
.members-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.member-row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) 128px auto;
  align-items: start;
  gap: 10px;
}
.member-order {
  display: flex;
  flex-direction: column;
}
.member-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}
.member-error {
  font-size: 12px;
  color: var(--tf-danger);
  line-height: 1.5;
}
.member-row .el-tag {
  align-self: center;
}
@media (max-width: 560px) {
  .member-row {
    grid-template-columns: auto minmax(0, 1fr) 96px auto;
    gap: 6px;
  }
}
</style>
