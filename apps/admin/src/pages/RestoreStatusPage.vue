<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { request } from '../api'
import { adminBasePath } from '../config'
import { pendingRestore, saveRestore, type RestoreJob } from '../restore-state'

const job = ref<RestoreJob | null>(null)
const error = ref('')
const stopped = ref(false)
const labels = {
  queued: '等待正在进行的操作结束',
  preparing: '正在下载、校验并解密备份',
  restoring: '正在替换当前数据库',
  succeeded: '数据库恢复成功',
  failed: '数据库恢复失败',
}
const title = computed(() =>
  job.value
    ? labels[job.value.state]
    : pendingRestore.value
      ? '正在查询恢复进度'
      : '没有待查询的恢复任务',
)
const finished = computed(() => job.value?.state === 'succeeded' || job.value?.state === 'failed')
let timer: ReturnType<typeof setTimeout> | undefined
async function poll() {
  const current = pendingRestore.value
  if (!current || stopped.value) return
  try {
    const { data } = await request<{ data: RestoreJob }>(`/restores/${current.id}`, {
      headers: { 'X-Restore-Token': current.token },
    })
    job.value = data
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : '暂时无法查询，正在重试。'
  }
  if (!stopped.value && !finished.value) timer = setTimeout(poll, 2000)
}
function exit() {
  saveRestore(null)
  window.location.assign(`${adminBasePath}login`)
}
onMounted(poll)
onBeforeUnmount(() => {
  stopped.value = true
  clearTimeout(timer)
})
</script>

<template>
  <main class="restore-status">
    <section aria-live="polite">
      <p class="eyebrow">Tripfolio · 数据库恢复</p>
      <h1>{{ title }}</h1>
      <template v-if="job?.state === 'succeeded'">
        <el-alert title="当前数据库已被所选备份替换" type="success" :closable="false" show-icon />
        <p>
          所有旧登录会话已失效，请使用备份时的账号和密码重新登录。自动备份已关闭，可在备份设置中重新开启。
        </p>
      </template>
      <template v-else-if="job?.state === 'failed'">
        <el-alert :title="job.message" type="error" :closable="false" show-icon />
        <p>恢复未提交，当前数据库的现有数据保持不变。</p>
      </template>
      <p v-else-if="pendingRestore">
        恢复期间暂停业务操作。请勿关闭或重启服务器，此页面会自动显示结果。
      </p>
      <p v-if="error" class="form-error" role="alert">
        {{ error }} 稍后将再次查询，请勿重复提交恢复。
      </p>
      <el-button v-if="finished || !pendingRestore" type="primary" @click="exit"
        >返回登录</el-button
      >
    </section>
  </main>
</template>

<style scoped>
.restore-status {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 24px;
  box-sizing: border-box;
  background: var(--el-fill-color-light);
}
section {
  width: min(100%, 620px);
  padding: 32px;
  box-sizing: border-box;
  border: 1px solid var(--el-border-color-light);
  border-radius: 12px;
  background: white;
}
h1 {
  font-size: 26px;
  margin: 12px 0 24px;
}
p {
  line-height: 1.8;
}
.eyebrow {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
</style>
