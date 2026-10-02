<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  loadRuntime,
  listJobs,
  listDeletionJobs,
  type RuntimeStatus,
  type JobPage,
  type DeletionJobPage,
} from '../api'
import { formatTime } from '../format'

const snapshot = ref<RuntimeStatus | null>(null)
const jobs = ref<JobPage | null>(null)
const deletions = ref<DeletionJobPage | null>(null)
const pending = reactive({ runtime: false, jobs: false, deletions: false })
const errors = reactive({ runtime: '', jobs: '', deletions: '' })
const request = { runtime: 0, jobs: 0, deletions: 0 }
const jobState = ref('')
const deletionState = ref('')
const jobPage = ref(1)
const deletionPage = ref(1)
const names: Record<string, string> = {
  api: '应用服务',
  database: '数据库',
  worker: '后台任务',
  object_store: '对象存储',
  maps: '地图服务',
  mail: '邮件服务',
  trip_purge: '旅行永久清理',
}
const states: Record<string, string> = {
  ok: '正常',
  degraded: '需要关注',
  unavailable: '不可用',
  configured: '已配置',
  disabled: '未启用',
  retryable: '等待重试',
  discarded: '重试已耗尽',
  queued: '排队中',
  running: '执行中',
  completed: '已完成',
  failed: '失败',
}
const kinds: Record<string, string> = {
  ping: '任务链路检查',
  trip_purge: '旅行清理',
  asset_verify: '文件校验',
  route_recalculate: '路线计算',
  unknown: '其他任务',
}
const stages: Record<string, string> = {
  revoke_access: '撤销访问',
  remove_objects: '清理存储对象',
  remove_rows: '清理关联数据',
  finalize: '收尾',
  done: '完成',
}
const pill = (state: string) =>
  state === 'ok' || state === 'completed'
    ? 'normal'
    : ['unavailable', 'discarded', 'failed', 'degraded'].includes(state)
      ? 'error-status'
      : 'muted-status'

function begin(section: keyof typeof request) {
  pending[section] = true
  errors[section] = ''
}
async function refreshRuntime() {
  const current = ++request.runtime
  snapshot.value = null
  begin('runtime')
  try {
    const value = await loadRuntime()
    if (current === request.runtime) snapshot.value = value
  } catch (error) {
    if (current === request.runtime)
      errors.runtime = error instanceof Error ? error.message : '运行状态加载失败。'
  } finally {
    if (current === request.runtime) pending.runtime = false
  }
}
async function refreshJobs(page = jobPage.value) {
  const current = ++request.jobs
  jobPage.value = page
  jobs.value = null
  begin('jobs')
  try {
    const value = await listJobs(jobState.value, page)
    if (current === request.jobs) jobs.value = value
  } catch (error) {
    if (current === request.jobs)
      errors.jobs = error instanceof Error ? error.message : '失败任务加载失败。'
  } finally {
    if (current === request.jobs) pending.jobs = false
  }
}
async function refreshDeletions(page = deletionPage.value) {
  const current = ++request.deletions
  deletionPage.value = page
  deletions.value = null
  begin('deletions')
  try {
    const value = await listDeletionJobs(deletionState.value, page)
    if (current === request.deletions) deletions.value = value
  } catch (error) {
    if (current === request.deletions)
      errors.deletions = error instanceof Error ? error.message : '清理进度加载失败。'
  } finally {
    if (current === request.deletions) pending.deletions = false
  }
}
function refreshAll() {
  void refreshRuntime()
  void refreshJobs()
  void refreshDeletions()
}
onMounted(refreshAll)
onBeforeUnmount(() => {
  ++request.runtime
  ++request.jobs
  ++request.deletions
})
</script>

<template>
  <section class="account-page">
    <div class="page-heading">
      <div>
        <p class="eyebrow">SERVICE STATUS</p>
        <h1>运行状态</h1>
        <p class="muted">查看服务依赖、失败任务与清理进度。</p>
      </div>
      <el-button :loading="pending.runtime || pending.jobs || pending.deletions" @click="refreshAll"
        >刷新</el-button
      >
    </div>
    <div v-if="errors.runtime" class="form-error" role="alert">
      {{ errors.runtime }}<el-button text @click="refreshRuntime">重试</el-button>
    </div>
    <div v-if="pending.runtime" class="empty-state" role="status">正在检查运行状态…</div>
    <template v-if="snapshot">
      <div class="section-heading">
        <h2>服务与依赖</h2>
        <span class="status-pill" :class="pill(snapshot.status)">{{
          states[snapshot.status]
        }}</span>
      </div>
      <ul class="runtime-components">
        <li v-for="component in snapshot.components" :key="component.name">
          <div>
            <strong>{{ names[component.name] }}</strong>
            <p>{{ component.summary }}</p>
          </div>
          <span class="status-pill" :class="pill(component.status)">{{
            states[component.status]
          }}</span>
        </li>
      </ul>
      <div class="overview-footer">
        <span>服务启动：{{ formatTime(snapshot.started_at) }}</span
        ><span>检查时间：{{ formatTime(snapshot.as_of) }}</span>
      </div>
      <dl class="runtime-counts">
        <div>
          <dt>执行中</dt>
          <dd>{{ snapshot.jobs.running.toLocaleString() }}</dd>
        </div>
        <div>
          <dt>待执行</dt>
          <dd>{{ snapshot.jobs.pending.toLocaleString() }}</dd>
        </div>
        <div>
          <dt>等待重试</dt>
          <dd>{{ snapshot.jobs.retryable.toLocaleString() }}</dd>
        </div>
        <div>
          <dt>重试已耗尽</dt>
          <dd>{{ snapshot.jobs.discarded.toLocaleString() }}</dd>
        </div>
        <div>
          <dt>待完成清理</dt>
          <dd>{{ snapshot.jobs.deletion_pending.toLocaleString() }}</dd>
        </div>
        <div>
          <dt>清理失败</dt>
          <dd>{{ snapshot.jobs.deletion_failed.toLocaleString() }}</dd>
        </div>
      </dl>
    </template>
    <section
      class="sessions-section"
      :aria-busy="pending.jobs"
      aria-labelledby="failed-jobs-heading"
    >
      <div class="section-heading">
        <div>
          <h2 id="failed-jobs-heading">失败任务</h2>
          <p class="muted">错误仅显示分类摘要。</p>
        </div>
        <el-select
          v-model="jobState"
          placeholder="全部失败任务"
          aria-label="失败任务状态"
          class="runtime-select"
          @change="refreshJobs(1)"
          ><el-option label="全部失败任务" value="" /><el-option
            label="等待重试"
            value="retryable" /><el-option label="重试已耗尽" value="discarded"
        /></el-select>
      </div>
      <div v-if="errors.jobs" class="form-error" role="alert">
        {{ errors.jobs }}<el-button text @click="refreshJobs()">重试</el-button>
      </div>
      <div v-if="pending.jobs" class="empty-state" role="status">正在加载失败任务…</div>
      <div v-if="jobs" class="table-panel">
        <el-table :data="jobs.data" row-key="id" empty-text="没有符合条件的失败任务">
          <el-table-column label="任务" min-width="155"
            ><template #default="{ row }"
              >{{ kinds[row.kind] }}
              <p class="user-email">编号 {{ row.id }}</p></template
            ></el-table-column
          >
          <el-table-column label="状态" min-width="115"
            ><template #default="{ row }"
              ><span class="status-pill" :class="pill(row.state)">{{
                states[row.state]
              }}</span></template
            ></el-table-column
          >
          <el-table-column label="已尝试 / 上限" width="125"
            ><template #default="{ row }"
              >{{ row.attempt }} / {{ row.max_attempts }}</template
            ></el-table-column
          >
          <el-table-column label="最近执行" min-width="175"
            ><template #default="{ row }"
              >{{ formatTime(row.attempted_at) }}
              <p v-if="row.state === 'retryable'" class="user-email">
                下次 {{ formatTime(row.scheduled_at) }}
              </p></template
            ></el-table-column
          >
          <el-table-column label="错误摘要" min-width="185"
            ><template #default="{ row }">{{
              row.error_summary || '未提供错误信息'
            }}</template></el-table-column
          >
        </el-table>
        <div class="table-footer">
          <span>共 {{ jobs.total.toLocaleString() }} 条</span
          ><el-pagination
            :current-page="jobPage"
            :page-size="20"
            :total="jobs.total"
            layout="prev, pager, next"
            :pager-count="5"
            @current-change="refreshJobs"
          />
        </div>
      </div>
    </section>
    <section
      class="sessions-section"
      :aria-busy="pending.deletions"
      aria-labelledby="deletion-jobs-heading"
    >
      <div class="section-heading">
        <div>
          <h2 id="deletion-jobs-heading">清理任务进度</h2>
          <p class="muted">只有任务显示“已完成”，才表示关联对象和数据已全部清理。</p>
        </div>
        <el-select
          v-model="deletionState"
          placeholder="全部清理任务"
          aria-label="清理任务状态"
          class="runtime-select"
          @change="refreshDeletions(1)"
          ><el-option label="全部清理任务" value="" /><el-option
            v-for="state in ['queued', 'running', 'completed', 'failed']"
            :key="state"
            :label="states[state]"
            :value="state"
        /></el-select>
      </div>
      <div v-if="errors.deletions" class="form-error" role="alert">
        {{ errors.deletions }}<el-button text @click="refreshDeletions()">重试</el-button>
      </div>
      <div v-if="pending.deletions" class="empty-state" role="status">正在加载清理进度…</div>
      <div v-if="deletions" class="table-panel">
        <el-table :data="deletions.data" row-key="id" empty-text="没有符合条件的清理任务">
          <el-table-column label="清理对象" min-width="210"
            ><template #default="{ row }"
              ><RouterLink
                v-if="row.target_trip_id && row.status !== 'completed'"
                class="text-link record-id"
                :to="{ name: 'trip', params: { id: row.target_trip_id } }"
                >旅行 {{ row.target_trip_id }}</RouterLink
              ><span v-else-if="row.target_trip_id" class="record-id"
                >旅行 {{ row.target_trip_id }}</span
              ><span v-else>账号清理</span>
              <p class="user-email">所属账号 {{ row.owner_account_id }}</p>
              <p class="user-email">任务 {{ row.id }}</p></template
            ></el-table-column
          >
          <el-table-column label="状态 / 阶段" min-width="140"
            ><template #default="{ row }"
              ><span class="status-pill" :class="pill(row.status)">{{ states[row.status] }}</span>
              <p class="user-email">{{ stages[row.stage] }}</p></template
            ></el-table-column
          >
          <el-table-column label="处理数量" min-width="120"
            ><template #default="{ row }"
              >{{ row.processed_items.toLocaleString() }} /
              {{
                row.total_items === null ? '总量待确定' : row.total_items.toLocaleString()
              }}</template
            ></el-table-column
          >
          <el-table-column label="创建时间" min-width="165"
            ><template #default="{ row }"
              >{{ formatTime(row.created_at) }}
              <p v-if="row.finished_at" class="user-email">
                结束 {{ formatTime(row.finished_at) }}
              </p></template
            ></el-table-column
          >
          <el-table-column label="错误摘要" min-width="180"
            ><template #default="{ row }">{{
              row.error_summary || '无'
            }}</template></el-table-column
          >
        </el-table>
        <div class="table-footer">
          <span>共 {{ deletions.total.toLocaleString() }} 条</span
          ><el-pagination
            :current-page="deletionPage"
            :page-size="20"
            :total="deletions.total"
            layout="prev, pager, next"
            :pager-count="5"
            @current-change="refreshDeletions"
          />
        </div>
      </div>
    </section>
  </section>
</template>
