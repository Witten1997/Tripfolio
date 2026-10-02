<script setup lang="ts">
import { computed, ref } from 'vue'
import { mutateTrip, reauthenticate, type AdminTripDetail } from '../api'

const props = defineProps<{ trip: AdminTripDetail['trip']; job: AdminTripDetail['purge_job'] }>()
const emit = defineEmits<{ changed: [message: string] }>()
const opened = ref(false)
const busy = ref(false)
const reason = ref('')
const password = ref('')
const confirm = ref(false)
const errorMessage = ref('')
const version = ref(0)
const retry = ref(false)
const canRequest = computed(() => !props.trip.purge_requested_at || !!props.job?.retryable)
const stages: Record<string, string> = {
  revoke_access: '撤销访问与等待上传授权到期',
  remove_objects: '清理对象存储',
  remove_rows: '清理关联数据',
  finalize: '最终校验',
  done: '清理完成',
}
const states: Record<string, string> = {
  queued: '等待执行',
  running: '正在清理',
  failed: '清理失败',
  completed: '清理完成',
}
function open() {
  retry.value = !!props.trip.purge_requested_at
  version.value = props.trip.version
  reason.value = password.value = errorMessage.value = ''
  confirm.value = false
  opened.value = true
}
async function submit() {
  if (busy.value || !confirm.value || !reason.value.trim() || !password.value) return
  busy.value = true
  errorMessage.value = ''
  try {
    await reauthenticate(password.value)
    await mutateTrip(props.trip.id, {
      action: retry.value ? 'retry' : 'purge',
      version: version.value,
      reason: reason.value.trim(),
      confirm: true,
    })
    opened.value = false
    emit('changed', '已受理清理请求。只有任务显示“清理完成”时，才表示关联对象和数据已全部清理。')
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '清理请求失败。'
  } finally {
    password.value = ''
    busy.value = false
  }
}
</script>

<template>
  <section v-if="trip.deleted_at" class="purge-controls" aria-label="永久清理">
    <h2>永久清理</h2>
    <template v-if="job">
      <p role="status">
        {{ states[job.status] || job.status }} · {{ stages[job.stage] || job.stage
        }}<span v-if="job.total_items !== null">
          · {{ job.processed_items }} / {{ job.total_items }}</span
        >
      </p>
      <p v-if="job.error_summary" class="form-error" role="alert">{{ job.error_summary }}</p>
      <p v-if="job.retryable && job.status !== 'failed'" class="muted">
        任务已中断，可复验密码后重试。
      </p>
      <RouterLink to="/runtime" class="text-link">查看运行状态与清理进度</RouterLink>
    </template>
    <p class="muted">永久清理不可恢复。旅行及关联内容会一起删除，审计历史保留。</p>
    <el-button v-if="canRequest" type="danger" plain @click="open">{{
      trip.purge_requested_at ? '重试永久清理' : '申请永久清理'
    }}</el-button>
    <el-dialog
      v-model="opened"
      :title="retry ? '重试永久清理' : '申请永久清理'"
      width="min(560px, 94vw)"
      :close-on-click-modal="false"
      :close-on-press-escape="!busy"
      :show-close="!busy"
      @closed="password = ''"
    >
      <form id="trip-purge-form" @submit.prevent="submit">
        <p>
          将永久删除“{{ trip.name }}”及其行程、物资、待办、成员、账目和关联文件。申请后不能恢复。
        </p>
        <label>操作原因<textarea v-model="reason" required maxlength="500" rows="3" /></label>
        <label
          >当前管理员密码<input
            v-model="password"
            type="password"
            autocomplete="current-password"
            required
            maxlength="1024"
        /></label>
        <label class="purge-confirm"
          ><input
            v-model="confirm"
            type="checkbox"
            required
          />我已确认影响范围，永久清理整趟旅行及其关联内容。</label
        >
        <div v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</div>
      </form>
      <template #footer>
        <el-button :disabled="busy" @click="opened = false">取消</el-button>
        <el-button
          type="danger"
          native-type="submit"
          form="trip-purge-form"
          :loading="busy"
          :disabled="!confirm || !reason.trim() || !password"
          >{{ retry ? '确认重试' : '确认永久清理' }}</el-button
        >
      </template>
    </el-dialog>
  </section>
</template>

<style scoped>
.purge-controls {
  margin-top: 28px;
  padding-top: 20px;
  border-top: 1px solid var(--el-border-color);
}
h2 {
  font-size: 18px;
}
label {
  display: grid;
  gap: 8px;
  margin-top: 16px;
}
input:not([type='checkbox']),
textarea {
  padding: 10px 12px;
  border: 1px solid var(--el-border-color);
  border-radius: 6px;
  background: var(--el-bg-color);
  color: var(--el-text-color-primary);
  font: inherit;
}
.purge-confirm {
  display: flex;
  align-items: flex-start;
}
</style>
