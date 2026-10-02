<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { mutateTrip, type AdminTripDetail, type AdminTripMutation } from '../api'
import { formatTime } from '../format'

const props = defineProps<{ trip: AdminTripDetail['trip'] }>()
const emit = defineEmits<{ changed: [message: string] }>()
const action = ref<AdminTripMutation['action'] | null>(null)
const busy = ref(false)
const errorMessage = ref('')
const notice = ref('')
const reason = ref('')
const version = ref(0)
const form = reactive({
  name: '',
  destination: '',
  start_date: '',
  end_date: '',
  timezone: '',
  currency_code: '',
  budget_amount: '',
})
let original = { ...form }
const title = computed(
  () =>
    ({
      edit: '编辑基础信息',
      archive: props.trip.archived_at ? '取消归档' : '归档旅行',
      trash: '移入回收站',
      restore: '恢复旅行',
      purge: '永久清理',
      retry: '重试清理',
    })[action.value || 'edit'],
)
const restorable = computed(
  () =>
    !!props.trip.deleted_at &&
    !props.trip.purge_requested_at &&
    !!props.trip.purge_after_at &&
    Date.parse(props.trip.purge_after_at) > Date.now(),
)
function open(value: AdminTripMutation['action']) {
  action.value = value
  version.value = props.trip.version
  reason.value = errorMessage.value = notice.value = ''
  Object.assign(form, {
    name: props.trip.name,
    destination: props.trip.destination,
    start_date: props.trip.start_date,
    end_date: props.trip.end_date,
    timezone: props.trip.timezone,
    currency_code: props.trip.currency_code,
    budget_amount: props.trip.budget_amount ?? '',
  })
  original = { ...form }
}
async function submit() {
  if (!action.value || busy.value || !reason.value.trim()) return
  busy.value = true
  errorMessage.value = ''
  const body: AdminTripMutation = {
    action: action.value,
    version: version.value,
    reason: reason.value.trim(),
    confirm: false,
  }
  if (action.value === 'archive') body.archived = !props.trip.archived_at
  if (action.value === 'edit') {
    body.changes = { budget_set: false }
    for (const key of [
      'name',
      'destination',
      'start_date',
      'end_date',
      'timezone',
      'currency_code',
    ] as const) {
      if (form[key] !== original[key]) body.changes[key] = form[key]
    }
    if (form.budget_amount !== original.budget_amount) {
      body.changes.budget_set = true
      body.changes.budget_amount = form.budget_amount.trim() || null
    }
  }
  try {
    const result = await mutateTrip(props.trip.id, body)
    const labels: Record<string, string> = {
      ITINERARY_OUTSIDE_TRIP_DATES: '部分行程日期在新的旅行日期之外。',
      TIMEZONE_INTERPRETATION_CHANGED: '已有本地时间将按新的旅行时区解释。',
    }
    notice.value = result.warnings.map((v) => labels[v] || v).join(' ') || '操作已完成。'
    action.value = null
    emit('changed', notice.value)
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '操作失败，请重试。'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="lifecycle-controls" aria-label="旅行管理操作">
    <div v-if="notice" class="notice-strip" role="status">{{ notice }}</div>
    <div v-if="!trip.deleted_at" class="lifecycle-actions">
      <el-button @click="open('edit')">编辑基础信息</el-button>
      <el-button @click="open('archive')">{{ trip.archived_at ? '取消归档' : '归档' }}</el-button>
      <el-button type="danger" plain @click="open('trash')">移入回收站</el-button>
    </div>
    <template v-else>
      <p class="muted">
        恢复截止：{{
          formatTime(trip.purge_after_at)
        }}。恢复会保留此前单独删除的内容状态，旧分享链接不会恢复。
      </p>
      <el-button :disabled="!restorable" @click="open('restore')">恢复旅行</el-button>
      <p v-if="!restorable" class="muted">
        {{ trip.purge_requested_at ? '已申请永久清理，不能恢复。' : '已超过 30 天恢复期限。' }}
      </p>
    </template>
    <el-dialog
      :model-value="action !== null"
      :title="title"
      width="min(620px, 94vw)"
      :close-on-click-modal="false"
      :close-on-press-escape="!busy"
      :show-close="!busy"
      @close="action = null"
    >
      <form id="trip-lifecycle-form" @submit.prevent="submit">
        <div v-if="action === 'edit'" class="lifecycle-form">
          <label>旅行名称<input v-model="form.name" required maxlength="120" /></label>
          <label>目的地<input v-model="form.destination" maxlength="300" /></label>
          <label
            >开始日期<input v-model="form.start_date" type="date" required :max="form.end_date"
          /></label>
          <label
            >结束日期<input v-model="form.end_date" type="date" required :min="form.start_date"
          /></label>
          <label
            >旅行时区<input
              v-model="form.timezone"
              required
              maxlength="64"
              placeholder="Asia/Shanghai"
          /></label>
          <label
            >币种<input v-model="form.currency_code" required maxlength="3" placeholder="CNY"
          /></label>
          <label
            >预算<input
              v-model="form.budget_amount"
              inputmode="decimal"
              placeholder="留空表示未设置"
          /></label>
        </div>
        <p v-if="action === 'trash'">
          整趟旅行将停止用户端和分享访问，30 天内可恢复。此前单独删除的内容仍保持删除状态。
        </p>
        <p v-if="action === 'restore'">恢复整趟旅行；保留管理分享限制，旧分享链接不会恢复。</p>
        <label class="lifecycle-reason"
          >操作原因<textarea
            v-model="reason"
            required
            maxlength="500"
            rows="3"
            placeholder="填写本次操作原因"
          />
        </label>
        <div v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</div>
      </form>
      <template #footer>
        <el-button :disabled="busy" @click="action = null">取消</el-button>
        <el-button
          type="primary"
          native-type="submit"
          form="trip-lifecycle-form"
          :loading="busy"
          :disabled="!reason.trim()"
          >确认{{ title }}</el-button
        >
      </template>
    </el-dialog>
  </section>
</template>

<style scoped>
.lifecycle-controls {
  margin-top: 24px;
}
.lifecycle-actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}
.lifecycle-actions .el-button {
  margin-left: 0;
}
.lifecycle-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
label {
  display: grid;
  gap: 8px;
}
input,
textarea {
  width: 100%;
  box-sizing: border-box;
  padding: 10px 12px;
  border: 1px solid var(--el-border-color);
  border-radius: 6px;
  color: var(--el-text-color-primary);
  background: var(--el-bg-color);
  font: inherit;
}
.lifecycle-reason {
  margin-top: 20px;
}
@media (max-width: 540px) {
  .lifecycle-form {
    grid-template-columns: 1fr;
  }
}
</style>
