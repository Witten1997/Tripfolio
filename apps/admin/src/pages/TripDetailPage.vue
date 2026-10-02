<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { loadTrip, type AdminTripDetail } from '../api'
import { formatTime, phaseLabel } from '../format'

const route = useRoute()
const id = computed(() => String(route.params.id))
const detail = ref<AdminTripDetail | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const countLabels: Record<keyof AdminTripDetail['counts'], string> = {
  itinerary: '行程',
  packing: '物资',
  todos: '待办',
  members: '成员',
}
let request = 0
async function refresh() {
  const current = ++request
  loading.value = true
  detail.value = null
  errorMessage.value = ''
  try {
    const response = await loadTrip(id.value)
    if (current === request) detail.value = response
  } catch (error) {
    if (current === request)
      errorMessage.value = error instanceof Error ? error.message : '旅行概况加载失败。'
  } finally {
    if (current === request) loading.value = false
  }
}
watch(id, refresh, { immediate: true })
onBeforeUnmount(() => {
  ++request
})
</script>

<template>
  <section class="account-page" :aria-busy="loading">
    <RouterLink to="/trips" class="text-link">← 旅行管理</RouterLink>
    <div class="page-heading trip-heading">
      <div>
        <p class="eyebrow">TRIP OVERVIEW</p>
        <h1>{{ detail?.trip.name || '旅行概况' }}</h1>
        <p v-if="detail" class="muted">
          所属用户：<RouterLink
            class="text-link"
            :to="{ name: 'trips', query: { account_id: detail.owner.id } }"
            >{{ detail.owner.nickname }} · {{ detail.owner.email }}</RouterLink
          >
        </p>
      </div>
      <el-button :loading="loading" @click="refresh">刷新</el-button>
    </div>
    <div v-if="errorMessage" class="form-error" role="alert">
      {{ errorMessage }}<el-button text @click="refresh">重试</el-button>
    </div>
    <div v-if="!detail && loading" class="empty-state" role="status">正在加载旅行…</div>
    <template v-if="detail">
      <div v-if="detail.trip.deleted_at" class="notice-strip">
        此旅行已于 {{ formatTime(detail.trip.deleted_at) }} 移入回收站。{{
          detail.trip.purge_requested_at ? '已申请永久清理。' : ''
        }}
      </div>
      <section class="trip-facts-panel" aria-label="旅行概况">
        <dl class="user-facts">
          <dt>目的地</dt>
          <dd>{{ detail.trip.destination || '未填写' }}</dd>
          <dt>日期</dt>
          <dd>{{ detail.trip.start_date }} 至 {{ detail.trip.end_date }}</dd>
          <dt>状态</dt>
          <dd>
            {{ phaseLabel(detail.phase) }} · {{ detail.trip.archived_at ? '已归档' : '未归档' }}
          </dd>
          <dt>预算</dt>
          <dd>
            {{
              detail.trip.budget_amount === null
                ? '未设置'
                : `${detail.trip.budget_amount} ${detail.trip.currency_code}`
            }}
          </dd>
          <dt>最近更新</dt>
          <dd>{{ formatTime(detail.trip.updated_at) }}</dd>
        </dl>
        <dl class="trip-counts" aria-label="旅行内容数量">
          <div v-for="(label, key) in countLabels" :key="key">
            <dt>{{ label }}</dt>
            <dd>{{ detail.counts[key].toLocaleString() }}</dd>
          </div>
        </dl>
      </section>
    </template>
  </section>
</template>
