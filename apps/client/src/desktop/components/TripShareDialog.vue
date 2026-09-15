<script setup lang="ts">
import { ElAlert, ElButton, ElDialog, ElInput, ElMessageBox, ElSkeleton } from 'element-plus'
import { ref } from 'vue'

import { useTripShare } from '@/shared/travel/useTripShare'

const props = defineProps<{ tripId: string }>()
const visible = ref(false)
const { status, share, busy, error, copied, load, enable, rotate, disable, copy } = useTripShare(
  props.tripId,
)

async function open() {
  visible.value = true
  await load()
}

async function confirmRotate() {
  try {
    await ElMessageBox.confirm(
      '旧链接会立即失效，已经拿到旧链接的人将无法再打开。',
      '重新生成分享链接',
      {
        confirmButtonText: '重新生成',
        cancelButtonText: '取消',
        type: 'warning',
      },
    )
  } catch {
    return
  }
  await rotate()
}

async function confirmDisable() {
  try {
    await ElMessageBox.confirm(
      '关闭后链接立即失效；之后可以再次开启，但会得到一条新链接。',
      '关闭分享',
      {
        confirmButtonText: '关闭分享',
        cancelButtonText: '取消',
        type: 'warning',
      },
    )
  } catch {
    return
  }
  await disable()
}

function selectAll(event: FocusEvent) {
  ;(event.target as HTMLInputElement | null)?.select()
}

defineExpose({ open })
</script>

<template>
  <ElDialog
    v-model="visible"
    title="分享旅行"
    width="min(520px, 92vw)"
    align-center
    destroy-on-close
  >
    <ElSkeleton v-if="status === 'idle' || status === 'loading'" :rows="2" animated />
    <template v-else>
      <ElAlert
        v-if="error"
        :title="error"
        type="error"
        show-icon
        :closable="false"
        class="share-alert"
      />
      <p v-if="status !== 'on'" class="share-hint">
        生成一条链接后，任何拿到链接的人无需登录即可查看这趟旅行的行程与地图，但不能编辑。账单、行李清单、待办、相册与备注不会被分享。
      </p>
      <template v-else-if="share">
        <ElInput :model-value="share.url" readonly aria-label="分享链接" @focus="selectAll" />
        <p class="share-meta">
          <span>链接已被打开 {{ share.view_count }} 次</span>
          <span v-if="copied" role="status" class="share-copied">已复制</span>
        </p>
      </template>
    </template>
    <template #footer>
      <div class="share-actions tf-actions">
        <ElButton v-if="status === 'error'" @click="load">重试</ElButton>
        <ElButton v-else-if="status === 'off'" type="primary" :loading="busy" @click="enable"
          >生成链接</ElButton
        >
        <template v-else-if="status === 'on'">
          <ElButton type="primary" :disabled="busy" @click="copy">复制链接</ElButton>
          <ElButton :loading="busy" @click="confirmRotate">重新生成</ElButton>
          <ElButton :loading="busy" @click="confirmDisable">关闭分享</ElButton>
        </template>
      </div>
    </template>
  </ElDialog>
</template>

<style scoped>
.share-alert {
  margin-bottom: 12px;
}
.share-hint {
  margin: 0;
  color: var(--tf-text-2);
  line-height: 1.6;
}
.share-meta {
  display: flex;
  justify-content: space-between;
  margin: 10px 0 0;
  font-size: 12px;
  color: var(--tf-text-3);
}
.share-copied {
  color: var(--tf-success);
}
.share-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}
</style>
