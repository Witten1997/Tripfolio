<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { components } from '@tripfolio/contracts/openapi/admin'
import { request } from '../api'
import AdminControlDialog from '../components/AdminControlDialog.vue'

type Settings = components['schemas']['BackupSettings']
type Run = components['schemas']['BackupRun']
type Page = components['schemas']['BackupPage']
const settings = ref<Settings | null>(null)
const records = ref<Page | null>(null)
const form = reactive({
  enabled: false,
  time: '03:00',
  retain: 7,
  url: '',
  username: '',
  password: '',
})
const loading = ref(true)
const error = ref('')
const historyError = ref('')
const historyLoading = ref(false)
const page = ref(1)
const dialog = ref(false)
const action = ref<'save' | 'test' | 'run' | 'retry'>('save')
const selected = ref<Run | null>(null)
const labels = { save: '保存备份设置', test: '测试 WebDAV', run: '立即备份', retry: '重试备份' }
const descriptions = {
  save: '保存定时计划与存储目标。WebDAV 密码留空将保留原密码，更换目标或用户名时需要重新填写。',
  test: '使用已保存的设置创建临时文件，验证上传、读取、完整性校验和删除权限。',
  run: '将当前数据库的完整业务数据加密备份到已保存的 WebDAV 目标。',
  retry: '优先重用校验通过的暂存文件；暂存文件已失效时重新导出数据库。',
}
const states: Record<string, string> = {
  queued: '排队中',
  running: '正在导出',
  uploading: '正在上传',
  verifying: '正在校验',
  retrying: '等待重试',
  succeeded: '已备份',
  failed: '失败',
}
const messages: Record<string, string> = {
  BACKUP_INTERRUPTED: '任务中断，可重试',
  BACKUP_TIMEOUT: '任务超时或服务停止',
  BACKUP_KEY_MISSING: '服务器未配置备份密钥',
  BACKUP_KEY_INVALID: '备份密钥与已保存凭证不匹配',
  BACKUP_SPACE_LIMIT: '暂存空间不足或达到上限',
  BACKUP_TOO_LARGE: '超过单文件大小上限',
  BACKUP_VERSION_MISMATCH: '数据库客户端版本过旧',
  BACKUP_DATABASE_MISMATCH: '备份连接指向其他数据库',
  BACKUP_DUMP_FAILED: '数据库导出失败，请检查连接、权限及客户端',
  BACKUP_FILE_MISSING: '暂存文件不可用',
  BACKUP_AUDIT_FAILED: '状态或审计记录保存失败',
  WEBDAV_DIRECTORY_FAILED: '备份目录创建失败',
  WEBDAV_UPLOAD_FAILED: '上传失败，请检查空间与写入权限',
  WEBDAV_VERIFY_FAILED: '远端完整性校验失败',
  WEBDAV_DELETE_FAILED: '旧备份清理失败，请检查删除权限',
  WEBDAV_ADDRESS_BLOCKED: '内网地址未加入服务器白名单',
  WEBDAV_REQUEST_FAILED: 'WebDAV 请求失败，请检查连接与凭证',
}
const hasActive = computed(
  () => records.value?.data.some((r) => !['succeeded', 'failed'].includes(r.state)) ?? false,
)
const dirty = computed(
  () =>
    !!settings.value &&
    (form.password !== '' ||
      (['enabled', 'time', 'retain', 'url', 'username'] as const).some(
        (key) => form[key] !== settings.value?.[key],
      )),
)
const time = (value: string | null) =>
  value
    ? new Intl.DateTimeFormat('zh-CN', {
        dateStyle: 'short',
        timeStyle: 'medium',
        timeZone: 'Asia/Shanghai',
      }).format(new Date(value))
    : '—'
const size = (bytes: number) => (bytes ? `${(bytes / 1024 / 1024).toFixed(2)} MB` : '—')
const duration = (run: Run) => {
  if (!run.started_at) return '—'
  const end = run.finished_at ? new Date(run.finished_at).getTime() : Date.now()
  const seconds = Math.max(0, Math.ceil((end - new Date(run.started_at).getTime()) / 1000))
  if (seconds < 60) return `${seconds} 秒`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`
  return `${Math.floor(seconds / 3600)} 小时 ${Math.floor((seconds % 3600) / 60)} 分`
}
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
let historyRequest = 0

async function loadSettings() {
  settings.value = (await request<{ data: Settings }>('/backup-settings')).data
  const { enabled, time, retain, url, username } = settings.value
  Object.assign(form, { enabled, time, retain, url, username, password: '' })
}
async function loadHistory(targetPage = page.value, quiet = false) {
  const generation = ++historyRequest
  page.value = targetPage
  if (!quiet) historyLoading.value = true
  historyError.value = ''
  try {
    const result = await request<Page>(`/backups?page=${targetPage}`)
    if (!disposed && generation === historyRequest) records.value = result
  } catch (e) {
    if (!disposed && generation === historyRequest)
      historyError.value = e instanceof Error ? e.message : '备份记录加载失败'
  } finally {
    if (generation === historyRequest) historyLoading.value = false
  }
}
async function refresh() {
  loading.value = true
  error.value = ''
  try {
    await loadSettings()
    await loadHistory()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '备份设置加载失败'
  } finally {
    loading.value = false
  }
}
function open(next: typeof action.value, run: Run | null = null) {
  action.value = next
  selected.value = run
  dialog.value = true
}
async function execute(reason: string) {
  if (!settings.value) return
  if (action.value === 'save') {
    await request('/backup-settings', {
      method: 'PUT',
      body: JSON.stringify({ ...form, version: settings.value.version, reason }),
    })
    await loadSettings()
    ElMessage.success('备份设置已保存')
  } else if (action.value === 'test') {
    await request('/backup-settings/test', { method: 'POST', body: JSON.stringify({ reason }) })
    ElMessage.success('WebDAV 创建、上传、读取、校验及删除检查通过')
  } else {
    const path =
      action.value === 'retry' && selected.value
        ? `/backups/${selected.value.id}/retry`
        : '/backups'
    await request(path, { method: 'POST', body: JSON.stringify({ reason }) })
    await loadHistory(1)
    ElMessage.success('备份任务已入队')
  }
}
async function poll() {
  if (disposed) return
  if (!document.hidden) await loadHistory(page.value, true)
  if (!disposed) timer = setTimeout(poll, 10000)
}
onMounted(async () => {
  await refresh()
  if (!disposed) timer = setTimeout(poll, 10000)
})
onBeforeUnmount(() => {
  disposed = true
  clearTimeout(timer)
  historyRequest++
})
</script>

<template>
  <section class="account-page backup-page">
    <div class="page-heading">
      <div>
        <p class="eyebrow">数据保护</p>
        <h1>数据库备份</h1>
        <p class="muted">按北京时间定时备份，将加密文件保存到你的 WebDAV。</p>
      </div>
      <el-button :loading="loading" :disabled="dirty || dialog" @click="refresh">刷新</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    <p v-if="loading && !settings" role="status">正在加载备份设置…</p>
    <template v-if="settings">
      <el-alert
        v-if="!settings.ready"
        :title="settings.readiness"
        type="warning"
        :closable="false"
        show-icon
      />
      <div class="backup-columns">
        <section class="backup-panel" aria-labelledby="schedule-title">
          <h2 id="schedule-title">定时计划</h2>
          <el-form label-position="top" @submit.prevent="open('save')">
            <el-form-item label="自动备份"
              ><el-switch v-model="form.enabled" :disabled="dialog" aria-label="启用自动备份"
            /></el-form-item>
            <el-form-item label="每日执行时间（北京时间）"
              ><el-time-select
                v-model="form.time"
                start="00:00"
                end="23:59"
                step="00:01"
                :editable="false"
                :clearable="false"
                :disabled="dialog"
                aria-label="每日备份时间"
            /></el-form-item>
            <el-form-item label="保留最近几份成功备份"
              ><el-input-number
                v-model="form.retain"
                :min="1"
                :max="90"
                :precision="0"
                :disabled="dialog"
                aria-label="备份保留份数"
            /></el-form-item>
            <p class="muted">
              下次计划：{{ settings.enabled ? time(settings.next_at) : '未启用' }}
            </p>
            <p class="muted">
              服务恢复后最多补一次错过的计划。新备份完成并校验后，才会清理同一存储目标中的旧备份。
            </p>
          </el-form>
        </section>
        <section class="backup-panel" aria-labelledby="storage-title">
          <h2 id="storage-title">WebDAV 存储</h2>
          <el-form label-position="top" @submit.prevent="open('save')">
            <el-form-item label="已存在的 WebDAV 目录 URL"
              ><el-input
                v-model="form.url"
                placeholder="https://dav.example.com/backups/"
                :maxlength="2048"
                :disabled="dialog"
                aria-label="WebDAV 目录 URL"
            /></el-form-item>
            <el-form-item label="用户名"
              ><el-input
                v-model="form.username"
                autocomplete="off"
                :maxlength="256"
                :disabled="dialog"
                aria-label="WebDAV 用户名"
            /></el-form-item>
            <el-form-item label="密码或应用密码"
              ><el-input
                v-model="form.password"
                type="password"
                autocomplete="new-password"
                :placeholder="settings.password_set ? '已保存，留空保持原密码' : '输入 WebDAV 密码'"
                :maxlength="2048"
                :disabled="dialog"
                show-password
                aria-label="WebDAV 密码"
            /></el-form-item>
            <p class="muted">
              使用 HTTPS。内网目标需由服务器配置允许的主机；每个存储目标使用独立目录。
            </p>
          </el-form>
        </section>
      </div>
      <div class="backup-actions">
        <el-button type="primary" :disabled="!dirty || dialog" @click="open('save')"
          >保存设置</el-button
        >
        <el-button :disabled="!settings.password_set || dirty || dialog" @click="open('test')"
          >测试 WebDAV</el-button
        >
        <el-button
          :disabled="!settings.ready || !settings.password_set || dirty || hasActive || dialog"
          @click="open('run')"
          >立即备份</el-button
        >
        <span v-if="dirty" class="muted">有未保存的设置</span>
      </div>
      <p class="backup-note">
        备份包含数据库结构与业务数据，不包含照片、附件等对象存储文件。文件使用 age
        加密，请单独保管服务器备份密钥；丢失密钥将无法恢复。
      </p>
    </template>
    <section class="backup-panel history" aria-labelledby="history-title">
      <div class="history-heading">
        <h2 id="history-title">备份记录</h2>
        <el-button :loading="historyLoading" @click="loadHistory()">刷新记录</el-button>
      </div>
      <p v-if="historyError" class="form-error" role="alert">{{ historyError }}</p>
      <el-table
        v-loading="historyLoading"
        :data="records?.data ?? []"
        empty-text="暂无备份记录"
        row-key="id"
      >
        <el-table-column label="开始时间" min-width="175"
          ><template #default="{ row }">{{ time(row.started_at) }}</template></el-table-column
        >
        <el-table-column label="数据快照" min-width="175"
          ><template #default="{ row }">{{ time(row.snapshot_at) }}</template></el-table-column
        >
        <el-table-column label="方式" width="80"
          ><template #default="{ row }">{{
            row.trigger === 'scheduled' ? '定时' : '手动'
          }}</template></el-table-column
        >
        <el-table-column label="状态" min-width="115"
          ><template #default="{ row }"
            ><el-tag
              :type="
                row.state === 'succeeded' ? 'success' : row.state === 'failed' ? 'danger' : 'info'
              "
              >{{ row.remote_deleted_at ? '已按规则清理' : states[row.state] }}</el-tag
            ></template
          ></el-table-column
        >
        <el-table-column label="大小" width="110"
          ><template #default="{ row }">{{ size(row.size_bytes) }}</template></el-table-column
        >
        <el-table-column label="耗时" min-width="130"
          ><template #default="{ row }">{{ duration(row) }}</template></el-table-column
        >
        <el-table-column label="结果" min-width="230"
          ><template #default="{ row }"
            ><span v-if="row.error_code || row.cleanup_error">{{
              messages[row.error_code || row.cleanup_error] || '执行失败，请检查配置及连接后重试'
            }}</span
            ><span v-else class="muted">{{
              row.state === 'succeeded' ? '远端 SHA-256 校验通过' : '任务进行中'
            }}</span></template
          ></el-table-column
        >
        <el-table-column label="操作" width="90" fixed="right"
          ><template #default="{ row }"
            ><el-button
              v-if="row.state === 'failed'"
              link
              type="primary"
              :disabled="hasActive || dirty || dialog"
              @click="open('retry', row)"
              >重试</el-button
            ></template
          ></el-table-column
        >
      </el-table>
      <el-pagination
        v-if="records && records.total > 20"
        class="backup-pagination"
        layout="prev, pager, next, total"
        :total="records.total"
        :page-size="20"
        :current-page="page"
        @current-change="loadHistory"
      />
    </section>
    <AdminControlDialog
      v-model="dialog"
      :title="labels[action]"
      :description="descriptions[action]"
      :execute="execute"
    />
  </section>
</template>

<style scoped>
.backup-page {
  display: grid;
  gap: 24px;
}
.page-heading,
.history-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 0;
}
.page-heading h1 {
  margin: 4px 0 12px;
}
.eyebrow {
  color: var(--el-color-primary);
  font-size: 12px;
  letter-spacing: 0.08em;
}
.backup-columns {
  display: grid;
  grid-template-columns: minmax(250px, 0.8fr) minmax(300px, 1.2fr);
  gap: 24px;
}
.backup-panel {
  padding: 24px;
  border: 1px solid var(--el-border-color-light);
  border-radius: 12px;
  background: #fff;
  min-width: 0;
}
.backup-panel h2 {
  margin: 0 0 24px;
  font-size: 18px;
}
.backup-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}
.backup-actions .el-button + .el-button {
  margin-left: 0;
}
.backup-note,
.muted {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  line-height: 1.75;
}
.backup-note {
  margin: 0;
}
.history-heading h2 {
  margin-bottom: 16px;
}
.backup-pagination {
  margin-top: 20px;
  justify-content: flex-end;
}
@media (max-width: 800px) {
  .backup-columns {
    grid-template-columns: 1fr;
  }
  .backup-panel {
    padding: 18px;
  }
}
</style>
