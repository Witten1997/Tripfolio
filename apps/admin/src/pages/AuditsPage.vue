<script setup lang="ts">
import { onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { listAudits, type AuditEntry, type AuditPage } from '../api'

const route = useRoute()
const router = useRouter()
const defaults = {
  actor_account_id: '',
  subject_account_id: '',
  action: '',
  result: '',
  date_from: '',
  date_to: '',
}
const filters = reactive({ ...defaults })
const result = ref<AuditPage | null>(null)
const selected = ref<AuditEntry | null>(null)
const drawer = ref(false)
const loading = ref(false)
const errorMessage = ref('')
const page = ref(1)
const asOf = ref('')
let request = 0
const actions: Record<string, string> = {
  login: '后台登录',
  'login.rate_limited': '登录限流',
  authentication: '身份验证',
  'request.invalid': '无效请求',
  'request.origin': '来源验证',
  'request.csrf': '请求防伪验证',
  'session.current': '读取管理身份',
  'session.list': '查询后台会话',
  'session.revoke': '撤销会话或退出',
  reauthenticate: '密码复验',
  'reauthenticate.rate_limited': '密码复验限流',
  'principal.grant': '授予管理资格',
  'principal.revoke': '撤销管理资格',
  'overview.read': '平台概览',
  'user.list': '查询用户',
  'user.read': '查看用户',
  'trip.list': '查询旅行',
  'trip.read': '查看旅行概况',
  'audit.list': '查询审计',
  'runtime.read': '查看运行状态',
  'job.list': '查询失败任务',
  'deletion_job.list': '查询清理进度',
}
const results: Record<string, string> = { success: '成功', failure: '失败', denied: '已拒绝' }
const resources: Record<string, string> = {
  account: '账号',
  trip: '旅行',
  admin_session: '后台会话',
  admin_principal: '管理资格',
  admin_audit: '审计记录',
  job: '后台任务',
  deletion_job: '清理任务',
  unknown: '其他资源',
}
function showDetail(entry: AuditEntry) {
  selected.value = entry
  drawer.value = true
}
const time = (value: string) =>
  new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'medium',
    timeZone: 'Asia/Shanghai',
  }).format(new Date(value))

async function refresh() {
  const current = ++request
  loading.value = true
  result.value = null
  errorMessage.value = ''
  drawer.value = false
  selected.value = null
  try {
    const response = await listAudits({
      ...filters,
      page: String(page.value),
      page_size: '20',
      as_of: asOf.value,
    })
    if (current === request) {
      result.value = response
      asOf.value = response.as_of
    }
  } catch (error) {
    if (current === request)
      errorMessage.value = error instanceof Error ? error.message : '审计加载失败。'
  } finally {
    if (current === request) loading.value = false
  }
}
async function search(value = 1, retainSnapshot = false) {
  const query = { ...filters, page: String(value), as_of: retainSnapshot ? asOf.value : '' }
  asOf.value = query.as_of
  if (router.resolve({ name: 'audits', query }).fullPath === route.fullPath) await refresh()
  else await router.replace({ name: 'audits', query })
}
function reset() {
  Object.assign(filters, defaults)
  void search()
}
watch(
  () => route.fullPath,
  () => {
    if (route.name !== 'audits') return
    for (const key of Object.keys(defaults) as (keyof typeof filters)[])
      filters[key] = typeof route.query[key] === 'string' ? route.query[key] : defaults[key]
    page.value = Number(route.query.page || 1)
    asOf.value = typeof route.query.as_of === 'string' ? route.query.as_of : ''
    void refresh()
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  ++request
})
</script>

<template>
  <section class="account-page" :aria-busy="loading">
    <div class="page-heading">
      <div>
        <p class="eyebrow">AUDIT LOG</p>
        <h1>审计中心</h1>
        <p class="muted">查询访问与操作记录，日期和时间均按北京时间显示。</p>
      </div>
      <el-button :loading="loading" @click="search()">刷新</el-button>
    </div>
    <form class="user-filters" @submit.prevent="search()">
      <div class="search-field">
        <label for="audit-actor" class="small-label">操作者账号编号</label
        ><el-input
          id="audit-actor"
          v-model="filters.actor_account_id"
          placeholder="完整账号编号"
          clearable
        />
      </div>
      <div class="search-field">
        <label for="audit-subject" class="small-label">对象所属账号编号</label
        ><el-input
          id="audit-subject"
          v-model="filters.subject_account_id"
          placeholder="完整账号编号"
          clearable
        />
      </div>
      <div class="audit-action-field">
        <label for="audit-action" class="small-label">操作</label
        ><el-select id="audit-action" v-model="filters.action" placeholder="全部操作"
          ><el-option label="全部操作" value="" /><el-option
            v-for="(label, value) in actions"
            :key="value"
            :label="label"
            :value="value"
        /></el-select>
      </div>
      <div class="status-field">
        <label for="audit-result" class="small-label">结果</label
        ><el-select id="audit-result" v-model="filters.result" placeholder="全部结果"
          ><el-option label="全部结果" value="" /><el-option
            v-for="(label, value) in results"
            :key="value"
            :label="label"
            :value="value"
        /></el-select>
      </div>
      <div class="date-field">
        <label for="audit-from" class="small-label">日期起</label
        ><input
          id="audit-from"
          v-model="filters.date_from"
          type="date"
          :max="filters.date_to || undefined"
        />
      </div>
      <div class="date-field">
        <label for="audit-to" class="small-label">日期止</label
        ><input
          id="audit-to"
          v-model="filters.date_to"
          type="date"
          :min="filters.date_from || undefined"
        />
      </div>
      <el-button type="primary" native-type="submit" :loading="loading">查询</el-button
      ><el-button @click="reset">重置</el-button>
    </form>
    <div v-if="errorMessage" class="form-error" role="alert">
      {{ errorMessage }}<el-button text @click="refresh">重试</el-button>
    </div>
    <div v-if="loading" class="empty-state" role="status">正在加载审计记录…</div>
    <div v-if="result" class="table-panel">
      <el-table :data="result.data" row-key="id" empty-text="没有符合条件的审计记录">
        <el-table-column label="时间" min-width="175"
          ><template #default="{ row }">{{ time(row.occurred_at) }}</template></el-table-column
        >
        <el-table-column label="操作" min-width="155"
          ><template #default="{ row }"
            ><span>{{ actions[row.action] || '其他操作' }}</span>
            <p v-if="row.summary && row.summary !== actions[row.action]" class="user-email">
              {{ row.summary }}
            </p></template
          ></el-table-column
        >
        <el-table-column label="操作者" min-width="175"
          ><template #default="{ row }"
            ><span class="record-id">{{
              row.actor_account_id || '未登录或本地维护'
            }}</span></template
          ></el-table-column
        >
        <el-table-column label="对象所属账号" min-width="175"
          ><template #default="{ row }"
            ><span class="record-id">{{
              row.subject_account_id || '跨账号查询或未指定'
            }}</span></template
          ></el-table-column
        >
        <el-table-column label="结果" width="90"
          ><template #default="{ row }"
            ><span
              class="status-pill"
              :class="row.result === 'success' ? 'normal' : 'error-status'"
              >{{ results[row.result] }}</span
            ></template
          ></el-table-column
        >
        <el-table-column label="记录" width="80" fixed="right"
          ><template #default="{ row }"
            ><el-button link type="primary" @click="showDetail(row)">详情</el-button></template
          ></el-table-column
        >
      </el-table>
      <div class="table-footer">
        <span>共 {{ result.total.toLocaleString() }} 条记录</span
        ><el-pagination
          :current-page="page"
          :page-size="20"
          :total="result.total"
          layout="prev, pager, next"
          :pager-count="5"
          @current-change="(value: number) => search(value, true)"
        />
      </div>
    </div>
    <el-drawer v-model="drawer" title="审计记录" size="min(540px, 100%)" @closed="selected = null">
      <dl v-if="selected" class="user-facts">
        <dt>记录编号</dt>
        <dd>{{ selected.id }}</dd>
        <dt>时间</dt>
        <dd>{{ time(selected.occurred_at) }}</dd>
        <dt>操作</dt>
        <dd>{{ actions[selected.action] || '其他操作' }}</dd>
        <dt>结果</dt>
        <dd>{{ results[selected.result] }}</dd>
        <dt>操作者</dt>
        <dd>{{ selected.actor_account_id || '未登录或本地维护' }}</dd>
        <dt>对象所属账号</dt>
        <dd>{{ selected.subject_account_id || '跨账号查询或未指定' }}</dd>
        <dt>资源类型</dt>
        <dd>{{ resources[selected.resource_type] || '无' }}</dd>
        <dt>资源编号</dt>
        <dd>{{ selected.resource_id || '无' }}</dd>
        <dt>请求编号</dt>
        <dd>{{ selected.request_id || '未提供或已隐藏' }}</dd>
        <dt>来源 IP</dt>
        <dd>{{ selected.source_ip || '无' }}</dd>
        <dt>摘要</dt>
        <dd>{{ selected.summary || '无' }}</dd>
      </dl>
    </el-drawer>
  </section>
</template>
