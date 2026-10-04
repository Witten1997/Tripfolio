<script setup lang="ts">
import { ElAlert, ElButton, ElSkeleton } from 'element-plus'
import { onMounted, onBeforeUnmount, ref, shallowRef } from 'vue'
import { useRouter } from 'vue-router'
import DeletionJobStatus from '@/desktop/components/DeletionJobStatus.vue'
import { getAccountDeletion, type DeletionJob } from '@/shared/api/deletion'
import { ApiError } from '@/shared/api/problem'
import { logout } from '@/shared/api/auth'
import {
  readDeletion,
  saveDeletion,
  clearDeletion,
  type SavedDeletion,
} from '@/shared/deletion/storage'
import { useAccountDeletion } from '@/shared/deletion/useAccountDeletion'
import { useSessionStore } from '@/shared/stores/session'

const router = useRouter()
const session = useSessionStore()
const flow = useAccountDeletion()
const { busy, error: recoveryError, canRecover } = flow
const saved = shallowRef<SavedDeletion | null>(null)
const job = shallowRef<DeletionJob | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const checkedAt = ref<string | null>(null)
let timer: ReturnType<typeof setTimeout> | undefined
let generation = 0

function restore() {
  generation++
  clearTimeout(timer)
  loading.value = false
  job.value = null
  checkedAt.value = null
  error.value = null
  saved.value = readDeletion()
  if (saved.value?.kind === 'pending') flow.pending.value = saved.value
  else flow.pending.value = null
  void refresh()
}

async function refresh() {
  if (loading.value || saved.value?.kind !== 'receipt') return
  clearTimeout(timer)
  // 每次查询重新核对存储，账号切换或凭证轮换后不使用旧的内存副本。
  const current = readDeletion()
  if (
    current?.kind !== 'receipt' ||
    current.receipt.receipt_token !== saved.value.receipt.receipt_token
  ) {
    restore()
    return
  }
  const request = ++generation
  loading.value = true
  error.value = null
  try {
    const result = await getAccountDeletion(current.receipt)
    if (request !== generation) return
    job.value = result
    checkedAt.value = new Date().toLocaleString('zh-CN', { hour12: false })
  } catch (cause) {
    if (request !== generation) return
    if (cause instanceof ApiError && [401, 403, 404, 410].includes(cause.problem?.status ?? 0)) {
      const unavailable = {
        kind: cause.problem?.status === 410 ? ('expired' as const) : ('invalid' as const),
        accountId: current.accountId,
      }
      try {
        saveDeletion(unavailable)
      } catch {
        /* 不再发送已拒绝的凭证。 */
      }
      saved.value = unavailable
      job.value = null
    } else error.value = '暂时无法查询进度，注销任务可能仍在继续。请稍后刷新。'
  } finally {
    if (request === generation) {
      loading.value = false
      if (saved.value?.kind === 'receipt' && job.value?.status !== 'completed' && !document.hidden)
        timer = setTimeout(() => void refresh(), error.value ? 10000 : 3000)
    }
  }
}

async function recover() {
  if (await flow.recover()) restore()
}
async function leave() {
  generation++
  clearTimeout(timer)
  try {
    await logout()
  } catch {
    session.clear()
  }
  // 仅显式离开才移除待确认操作；没有凭证不代表注销已完成或已撤销。
  clearDeletion()
  await router.replace({ name: 'login' })
}
function visibility() {
  if (!document.hidden) void refresh()
  else clearTimeout(timer)
}
onMounted(() => {
  restore()
  window.addEventListener('storage', restore)
  document.addEventListener('visibilitychange', visibility)
})
onBeforeUnmount(() => {
  generation++
  clearTimeout(timer)
  window.removeEventListener('storage', restore)
  document.removeEventListener('visibilitychange', visibility)
})
</script>

<template>
  <div class="deletion-progress-page">
    <header>
      <p class="deletion-brand">Tripfolio</p>
      <h1>账号注销进度</h1>
      <p>此页面仅查询已有任务，注销申请无法撤销。</p>
    </header>
    <section class="deletion-progress-panel tf-surface" aria-label="注销任务状态">
      <template v-if="saved?.kind === 'receipt'">
        <ElAlert
          title="注销查询凭证已保存，普通登录会话已结束。"
          type="info"
          :closable="false"
          show-icon
        />
        <ElSkeleton v-if="loading && !job" :rows="4" animated />
        <DeletionJobStatus v-if="job" :job="job" />
        <ElAlert v-if="error" :title="error" type="error" :closable="false" show-icon />
        <p v-if="checkedAt" class="deletion-progress-hint">最近查询：{{ checkedAt }}</p>
        <p class="deletion-progress-hint">
          查询凭证有效至
          {{
            new Date(saved.receipt.receipt_expires_at).toLocaleString('zh-CN', { hour12: false })
          }}。清除浏览器数据或登录其他账号后，将无法继续使用此凭证。
        </p>
        <div class="tf-actions">
          <ElButton :loading="loading" @click="refresh">刷新进度</ElButton
          ><ElButton v-if="job?.status === 'completed'" @click="leave"
            >清除查询记录并返回登录</ElButton
          >
        </div>
      </template>
      <template v-else-if="saved?.kind === 'pending'">
        <ElAlert title="注销申请结果尚未确认" type="warning" :closable="false" show-icon />
        <p>
          请求响应可能丢失，账号可能已进入注销流程。使用原有效登录会话重试同一请求，可取回查询凭证，不会创建第二个注销任务。
        </p>
        <ElAlert v-if="recoveryError" :title="recoveryError" type="error" :closable="false" />
        <ElButton v-if="canRecover" type="primary" :loading="busy" @click="recover"
          >恢复任务并保存查询凭证</ElButton
        >
        <template v-else
          ><p>
            原登录会话已失效或因刷新页面丢失，无法补领查询凭证。服务端已有的清理仍会继续；不能据此判断注销失败或完成。
          </p>
          <ElButton @click="leave">清除本地待确认记录并返回登录</ElButton></template
        >
      </template>
      <template v-else>
        <ElAlert
          :title="
            saved?.kind === 'expired'
              ? '查询凭证已过期'
              : saved?.kind === 'invalid'
                ? '查询凭证已失效'
                : '此浏览器没有可用的注销查询凭证'
          "
          type="warning"
          :closable="false"
          show-icon
        />
        <p>
          无法继续查询，不代表注销失败或撤销。请使用原先保存凭证的浏览器查看；完成后的查询记录最多保留七天。
        </p>
        <ElButton @click="leave">返回登录</ElButton>
      </template>
    </section>
  </div>
</template>

<style scoped>
.deletion-progress-page {
  display: flex;
  flex-direction: column;
  gap: 24px;
}
.deletion-progress-page h1 {
  margin: 8px 0;
  font-size: 28px;
}
.deletion-progress-page p {
  line-height: 1.8;
  overflow-wrap: anywhere;
}
.deletion-progress-page header p {
  color: var(--tf-text-3);
  margin: 0;
}
.deletion-brand {
  font-weight: 700;
}
.deletion-progress-panel {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 18px;
  padding: clamp(18px, 4vw, 28px);
  border-radius: var(--tf-radius-card);
}
.deletion-progress-panel p {
  margin: 0;
}
.deletion-progress-hint {
  font-size: 12px;
  color: var(--tf-text-3);
}
.deletion-progress-panel .tf-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}
.deletion-progress-panel :deep(.el-button) {
  white-space: normal;
  height: auto;
  min-height: 44px;
  line-height: 1.6;
}
.deletion-progress-panel :deep(.el-button + .el-button) {
  margin-left: 0;
}
</style>
