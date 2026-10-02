<script setup lang="ts">
import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { controlSharing, loadSharingRestriction, type SharingRestriction } from '../controls-api'
import { formatTime } from '../format'
import AdminControlDialog from './AdminControlDialog.vue'

const props = defineProps<{ tripId: string; disabled?: boolean }>()
const emit = defineEmits<{ changed: [value: SharingRestriction] }>()
const state = ref<SharingRestriction | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const dialog = ref(false)
const target = ref<{ id: string; restricted: boolean; version: number } | null>(null)
let generation = 0
async function refresh() {
  const run = ++generation
  loading.value = true
  errorMessage.value = ''
  state.value = null
  try {
    const result = await loadSharingRestriction(props.tripId)
    if (run === generation) state.value = result
  } catch (error) {
    if (run === generation)
      errorMessage.value = error instanceof Error ? error.message : '分享限制加载失败。'
  } finally {
    if (run === generation) loading.value = false
  }
}
function open() {
  if (!state.value || props.disabled) return
  target.value = {
    id: props.tripId,
    restricted: !state.value.restricted,
    version: state.value.version,
  }
  dialog.value = true
}
async function execute(reason: string) {
  if (!target.value || target.value.id !== props.tripId || props.disabled)
    throw new Error('当前旅行已变更，请关闭弹窗后重试。')
  const updated = await controlSharing(
    target.value.id,
    target.value.restricted,
    target.value.version,
    reason,
  )
  if (updated.trip_id === props.tripId) state.value = updated
  emit('changed', updated)
  ElMessage.success(
    updated.restricted ? '已限制分享，旧链接已失效' : '已解除限制，用户需重新生成链接',
  )
}
watch(
  () => props.tripId,
  () => {
    dialog.value = false
    void refresh()
  },
  { immediate: true },
)
</script>

<template>
  <section class="sessions-section" aria-label="旅行分享管控" :aria-busy="loading">
    <div class="section-heading">
      <div>
        <h2>分享管控</h2>
        <p class="muted">管理限制独立于用户分享开关。解除后，用户需重新生成链接。</p>
      </div>
      <el-button :loading="loading" @click="refresh">刷新</el-button>
    </div>
    <p v-if="loading" role="status">正在读取分享限制…</p>
    <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
    <template v-if="state">
      <p>
        <strong>{{ state.restricted ? '已限制分享' : '未限制分享' }}</strong>
      </p>
      <p v-if="state.reason" class="muted">最近操作原因：{{ state.reason }}</p>
      <p v-if="state.changed_at" class="muted">更新时间：{{ formatTime(state.changed_at) }}</p>
      <el-button
        :disabled="disabled"
        :type="state.restricted ? 'primary' : 'danger'"
        @click="open"
        >{{ state.restricted ? '解除分享限制' : '限制分享' }}</el-button
      >
    </template>
    <AdminControlDialog
      v-model="dialog"
      :title="target?.restricted ? '限制分享' : '解除分享限制'"
      description="现有分享链接将永久失效。本操作需要填写原因并验证管理员密码。"
      :execute="execute"
    />
  </section>
</template>
