<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Users, Map, RefreshCw, ArrowUpRight } from '@lucide/vue'
import { loadOverview, type Overview } from '../api'
import { formatTime } from '../format'

const data = ref<Overview | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const maxSignups = computed(() =>
  Math.max(1, ...(data.value?.signups.map((item) => item.count) || [])),
)
async function refresh() {
  if (loading.value) return
  loading.value = true
  errorMessage.value = ''
  try {
    data.value = await loadOverview()
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : '数据加载失败，请重试。'
  } finally {
    loading.value = false
  }
}
onMounted(refresh)
</script>

<template>
  <section class="account-page" :aria-busy="loading">
    <div class="page-heading">
      <div>
        <p class="eyebrow">WORKSPACE OVERVIEW</p>
        <h1>平台概览</h1>
        <p class="muted">了解用户增长与旅行数据的当前状态。</p>
      </div>
      <el-button :loading="loading" @click="refresh"
        ><RefreshCw v-if="!loading" :size="15" aria-hidden="true" />刷新</el-button
      >
    </div>
    <div v-if="errorMessage" class="form-error" role="alert">
      {{ errorMessage }}<el-button text @click="refresh">重试</el-button>
    </div>
    <div v-if="!data && loading" class="empty-state" role="status">正在加载平台数据…</div>
    <template v-if="data">
      <div class="metric-grid">
        <article class="metric">
          <Users :size="20" aria-hidden="true" />
          <p>注册用户</p>
          <strong>{{ data.users.toLocaleString() }}</strong
          ><span>今日新增 {{ data.new_users_today.toLocaleString() }} 人</span>
        </article>
        <article class="metric">
          <Map :size="20" aria-hidden="true" />
          <p>旅行总量</p>
          <strong>{{ data.trips.toLocaleString() }}</strong
          ><span>包含归档，排除回收站</span>
        </article>
      </div>
      <section class="trend-panel" aria-labelledby="signup-title">
        <div class="section-heading">
          <div>
            <h2 id="signup-title">最近 7 天注册趋势</h2>
            <p class="muted">每日新增账号，按北京时间统计。</p>
          </div>
          <RouterLink to="/users" class="text-link"
            >查看用户<ArrowUpRight :size="16" aria-hidden="true"
          /></RouterLink>
        </div>
        <div
          class="signup-chart"
          role="img"
          :aria-label="data.signups.map((item) => `${item.date} 新增 ${item.count} 人`).join('；')"
        >
          <div v-for="day in data.signups" :key="day.date" class="signup-day">
            <strong>{{ day.count }}</strong>
            <div class="bar-space">
              <span :style="{ height: `${(day.count / maxSignups) * 100}%` }"></span>
            </div>
            <span>{{ day.date.slice(5).replace('-', '/') }}</span>
          </div>
        </div>
      </section>
      <div class="overview-footer">
        <p>
          待重试或已失败任务：<strong>{{ data.failed_jobs }}</strong>
        </p>
        <p>更新于 {{ formatTime(data.as_of) }}</p>
      </div>
    </template>
  </section>
</template>
