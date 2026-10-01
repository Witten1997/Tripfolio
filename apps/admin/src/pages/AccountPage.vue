<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Monitor, RefreshCw, ShieldCheck } from '@lucide/vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { identity, listSessions, revokeSession, type ManagedSession } from '../api'

const sessions = ref<ManagedSession[]>([])
const loading = ref(false)
const errorMessage = ref('')
const revoking = ref('')
const formatTime = (value: string) =>
  new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(
    new Date(value),
  )
async function refresh() {
  if (loading.value) return
  loading.value = true
  errorMessage.value = ''
  try {
    sessions.value = await listSessions()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '会话加载失败，请重试。'
  } finally {
    loading.value = false
  }
}
async function revoke(session: ManagedSession) {
  try {
    await ElMessageBox.confirm(
      '撤销后，该设备需要重新输入密码才能访问后台。',
      '退出此设备的后台登录？',
      { confirmButtonText: '确认退出', cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  revoking.value = session.id
  try {
    await revokeSession(session.id)
    ElMessage.success('该设备已退出后台')
    await refresh()
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : '退出设备失败，请重试。')
  } finally {
    revoking.value = ''
  }
}
onMounted(refresh)
</script>

<template>
  <section class="account-page">
    <div class="page-heading">
      <div>
        <p class="eyebrow">YOUR ACCOUNT</p>
        <h1>账号与安全</h1>
        <p class="muted">管理你的后台身份与登录设备。</p>
      </div>
      <ShieldCheck class="heading-icon" :size="40" aria-hidden="true" />
    </div>
    <section class="profile-strip" aria-label="当前后台账号">
      <div class="avatar" aria-hidden="true">{{ identity?.nickname.slice(0, 1) }}</div>
      <div class="profile-name">
        <h2>{{ identity?.nickname }}</h2>
        <p>{{ identity?.email }}</p>
      </div>
      <div class="profile-role">
        <span class="small-label">管理身份</span><strong>超级管理员</strong>
      </div>
      <div class="profile-expiry">
        <span class="small-label">本次登录有效至</span
        ><strong>{{ identity ? formatTime(identity.expires_at) : '—' }}</strong>
      </div>
    </section>
    <section class="sessions-section" aria-labelledby="sessions-title" :aria-busy="loading">
      <div class="section-heading">
        <div>
          <h2 id="sessions-title">后台登录设备</h2>
          <p class="muted">仅显示管理后台的有效会话，闲置 30 分钟后自动退出。</p>
        </div>
        <el-button :loading="loading" @click="refresh"
          ><RefreshCw v-if="!loading" :size="15" aria-hidden="true" />刷新</el-button
        >
      </div>
      <div v-if="errorMessage" class="form-error" role="alert">
        {{ errorMessage }}<el-button text @click="refresh">重新加载</el-button>
      </div>
      <div v-if="loading && !sessions.length" class="empty-state" role="status">
        正在加载登录设备…
      </div>
      <div v-else-if="!sessions.length && !errorMessage" class="empty-state">
        当前没有有效的后台会话。
      </div>
      <ul v-else class="session-list">
        <li v-for="session in sessions" :key="session.id" class="session-row">
          <div class="device-icon"><Monitor :size="22" aria-hidden="true" /></div>
          <div class="device-description">
            <div class="device-title">
              <strong>{{ session.current ? '当前设备' : '其他登录设备' }}</strong
              ><span v-if="session.current" class="current-badge">正在使用</span>
            </div>
            <p class="device-agent">{{ session.user_agent || '未提供设备信息' }}</p>
            <p class="device-meta">
              来源 {{ session.source_ip || '未知' }} · 最近访问
              {{ formatTime(session.last_seen_at) }}
            </p>
          </div>
          <el-button
            v-if="!session.current"
            :loading="revoking === session.id"
            :disabled="!!revoking"
            @click="revoke(session)"
            >退出设备</el-button
          >
          <span v-else class="current-label">当前会话</span>
        </li>
      </ul>
    </section>
  </section>
</template>
