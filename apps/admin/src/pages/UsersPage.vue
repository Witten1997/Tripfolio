<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Search, RefreshCw } from '@lucide/vue'
import { listUsers, loadUser, type UserPage, type UserDetail } from '../api'
import { formatBytes, formatTime, statusLabel, clientLabel } from '../format'

const query = ref('')
const status = ref('')
const registeredFrom = ref('')
const registeredTo = ref('')
const page = ref(1)
const result = ref<UserPage | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const drawer = ref(false)
const detail = ref<UserDetail | null>(null)
const detailError = ref('')
const detailLoading = ref(false)
const selectedID = ref('')
let listRequest = 0
let detailRequest = 0
async function refresh() {
  const requestID = ++listRequest
  loading.value = true
  result.value = null
  errorMessage.value = ''
  try {
    const response = await listUsers({
      q: query.value,
      status: status.value,
      registeredFrom: registeredFrom.value,
      registeredTo: registeredTo.value,
      page: page.value,
      pageSize: 20,
    })
    if (requestID === listRequest) result.value = response
  } catch (error) {
    if (requestID === listRequest)
      errorMessage.value = error instanceof Error ? error.message : '用户加载失败，请重试。'
  } finally {
    if (requestID === listRequest) loading.value = false
  }
}
function search() {
  page.value = 1
  void refresh()
}
function changePage(value: number) {
  page.value = value
  void refresh()
}
async function showUser(id: string) {
  const requestID = ++detailRequest
  selectedID.value = id
  drawer.value = true
  detail.value = null
  detailError.value = ''
  detailLoading.value = true
  try {
    const response = await loadUser(id)
    if (requestID === detailRequest) detail.value = response
  } catch (error) {
    if (requestID === detailRequest)
      detailError.value = error instanceof Error ? error.message : '详情加载失败，请重试。'
  } finally {
    if (requestID === detailRequest) detailLoading.value = false
  }
}
onMounted(refresh)
</script>

<template>
  <section class="account-page" :aria-busy="loading">
    <div class="page-heading">
      <div>
        <p class="eyebrow">PLATFORM USERS</p>
        <h1>用户管理</h1>
        <p class="muted">查看用户账号、旅行数量与登录情况。</p>
      </div>
      <el-button :loading="loading" @click="refresh"
        ><RefreshCw v-if="!loading" :size="15" aria-hidden="true" />刷新</el-button
      >
    </div>
    <form class="user-filters" @submit.prevent="search">
      <div class="search-field">
        <label class="small-label" for="user-search">搜索用户</label
        ><el-input
          id="user-search"
          v-model="query"
          placeholder="邮箱、昵称或账号编号"
          clearable
          :maxlength="254"
        />
      </div>
      <div class="status-field">
        <label class="small-label" for="user-status">账号状态</label
        ><el-select
          id="user-status"
          v-model="status"
          aria-label="账号状态"
          placeholder="全部状态"
          @change="search"
          ><el-option label="全部状态" value="" /><el-option
            label="正常"
            value="active" /><el-option label="注销中" value="deleting"
        /></el-select>
      </div>
      <div class="date-field">
        <label class="small-label" for="registered-from">注册日期起（北京）</label
        ><input
          id="registered-from"
          v-model="registeredFrom"
          type="date"
          :max="registeredTo || undefined"
        />
      </div>
      <div class="date-field">
        <label class="small-label" for="registered-to">注册日期止（北京）</label
        ><input
          id="registered-to"
          v-model="registeredTo"
          type="date"
          :min="registeredFrom || undefined"
        />
      </div>
      <el-button native-type="submit" type="primary" :loading="loading"
        ><Search v-if="!loading" :size="16" aria-hidden="true" />查询</el-button
      >
    </form>
    <div v-if="errorMessage" class="form-error" role="alert">
      {{ errorMessage }}<el-button text @click="refresh">重试</el-button>
    </div>
    <div class="table-panel">
      <el-table
        :data="result?.data || []"
        v-loading="loading"
        empty-text="没有符合条件的用户"
        row-key="id"
        style="width: 100%"
      >
        <el-table-column label="用户" min-width="220"
          ><template #default="{ row }"
            ><button class="user-name-button" @click="showUser(row.id)">{{ row.nickname }}</button>
            <p class="user-email">{{ row.email }}</p></template
          ></el-table-column
        >
        <el-table-column label="状态" width="110"
          ><template #default="{ row }"
            ><span :class="['status-pill', row.status === 'active' ? 'normal' : 'muted-status']">{{
              statusLabel(row.status)
            }}</span></template
          ></el-table-column
        >
        <el-table-column label="旅行" prop="trip_count" width="80" align="right" />
        <el-table-column label="文件容量" width="115" align="right"
          ><template #default="{ row }">{{
            formatBytes(row.asset_bytes)
          }}</template></el-table-column
        >
        <el-table-column label="注册时间" min-width="165"
          ><template #default="{ row }">{{ formatTime(row.created_at) }}</template></el-table-column
        >
        <el-table-column label="操作" width="95" fixed="right"
          ><template #default="{ row }"
            ><el-button
              link
              type="primary"
              @click="showUser(row.id)"
              :aria-label="`查看 ${row.nickname} 的详情`"
              >查看详情</el-button
            ></template
          ></el-table-column
        >
      </el-table>
      <div class="table-footer">
        <span>共 {{ result?.total ?? 0 }} 位用户</span
        ><el-pagination
          :current-page="page"
          :page-size="20"
          :total="result?.total ?? 0"
          layout="prev, pager, next"
          :pager-count="5"
          @current-change="changePage"
        />
      </div>
    </div>
    <el-drawer v-model="drawer" title="用户详情" class="user-drawer" size="min(560px, 100vw)">
      <div v-if="detailLoading" class="empty-state" role="status">正在加载用户详情…</div>
      <div v-if="detailError" class="form-error" role="alert">
        {{ detailError }}<el-button text @click="showUser(selectedID)">重试</el-button>
      </div>
      <template v-if="detail">
        <div class="detail-heading">
          <h2>{{ detail.account.nickname }}</h2>
          <span v-if="detail.account.is_super_admin" class="role-badge">超级管理员</span>
        </div>
        <dl class="user-facts">
          <dt>邮箱</dt>
          <dd>{{ detail.account.email }}</dd>
          <dt>账号编号</dt>
          <dd>{{ detail.account.id }}</dd>
          <dt>账号状态</dt>
          <dd>{{ statusLabel(detail.account.status) }}</dd>
          <dt>注册时间</dt>
          <dd>{{ formatTime(detail.account.created_at) }}</dd>
          <dt>最近用户端访问</dt>
          <dd>{{ formatTime(detail.account.last_seen_at) }}</dd>
          <dt>旅行数量</dt>
          <dd>{{ detail.account.trip_count }}</dd>
          <dt>可用文件数量</dt>
          <dd>{{ detail.account.asset_count }}</dd>
          <dt>文件容量</dt>
          <dd>{{ formatBytes(detail.account.asset_bytes) }}</dd>
        </dl>
        <RouterLink
          class="text-link"
          :to="{ name: 'trips', query: { account_id: detail.account.id } }"
          >查看该用户的旅行 →</RouterLink
        >
        <h3 class="detail-section-title">有效用户端会话</h3>
        <p v-if="!detail.sessions.length" class="muted">暂无有效的用户端登录会话。</p>
        <ul v-else class="user-session-list">
          <li v-for="session in detail.sessions" :key="session.id">
            <strong>{{ session.device_name || clientLabel(session.client_kind) }}</strong>
            <p>最近访问 {{ formatTime(session.last_seen_at) }}</p>
            <p>到期时间 {{ formatTime(session.expires_at) }}</p>
          </li>
        </ul>
      </template>
    </el-drawer>
  </section>
</template>
