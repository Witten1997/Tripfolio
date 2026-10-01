<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { loadTrip, loadTripContent, type AdminTripDetail, type AdminContentPage } from '../api'
import { formatTime, phaseLabel } from '../format'
import TripContentList from '../components/TripContentList.vue'

const route = useRoute()
const id = computed(() => String(route.params.id))
const detail = ref<AdminTripDetail | null>(null)
const content = ref<AdminContentPage | null>(null)
const kind = ref<AdminContentPage['kind']>('itinerary')
const page = ref(1)
const loading = ref(false)
const contentLoading = ref(false)
const errorMessage = ref('')
const contentError = ref('')
let detailRequest = 0
let contentRequest = 0
async function refreshContent() {
  const current = ++contentRequest
  contentLoading.value = true
  content.value = null
  contentError.value = ''
  try {
    const response = await loadTripContent(id.value, kind.value, page.value)
    if (current === contentRequest) content.value = response
  } catch (error) {
    if (current === contentRequest)
      contentError.value = error instanceof Error ? error.message : '内容加载失败。'
  } finally {
    if (current === contentRequest) contentLoading.value = false
  }
}
async function refresh() {
  const current = ++detailRequest
  ++contentRequest
  loading.value = true
  detail.value = null
  content.value = null
  errorMessage.value = ''
  page.value = 1
  try {
    const response = await loadTrip(id.value)
    if (current === detailRequest) {
      detail.value = response
      await refreshContent()
    }
  } catch (error) {
    if (current === detailRequest)
      errorMessage.value = error instanceof Error ? error.message : '详情加载失败。'
  } finally {
    if (current === detailRequest) loading.value = false
  }
}
function changeKind() {
  page.value = 1
  void refreshContent()
}
function changePage(value: number) {
  page.value = value
  void refreshContent()
}
watch(id, refresh, { immediate: true })
</script>

<template>
  <section class="account-page" :aria-busy="loading">
    <RouterLink to="/trips" class="text-link">← 旅行管理</RouterLink>
    <div class="page-heading trip-heading">
      <div>
        <p class="eyebrow">TRIP DETAILS</p>
        <h1>{{ detail?.trip.name || '旅行详情' }}</h1>
        <p v-if="detail" class="muted">
          所属用户：{{ detail.owner.nickname }} · {{ detail.owner.email }}
        </p>
      </div>
      <el-button :loading="loading" @click="refresh">刷新</el-button>
    </div>
    <div v-if="errorMessage" class="form-error" role="alert">
      {{ errorMessage }}<el-button text @click="refresh">重试</el-button>
    </div>
    <div v-if="!detail && loading" class="empty-state">正在加载旅行…</div>
    <template v-if="detail">
      <div v-if="detail.trip.deleted_at" class="notice-strip">
        此旅行已于 {{ formatTime(detail.trip.deleted_at) }} 移入回收站。{{
          detail.trip.purge_requested_at ? '已申请永久清理。' : ''
        }}
      </div>
      <section class="trip-facts-panel" aria-label="旅行基础信息">
        <dl class="user-facts">
          <dt>旅行编号</dt>
          <dd>{{ detail.trip.id }}</dd>
          <dt>所属账号</dt>
          <dd>
            <RouterLink
              class="text-link"
              :to="{ name: 'trips', query: { account_id: detail.owner.id } }"
              >{{ detail.owner.id }}</RouterLink
            >
          </dd>
          <dt>日期</dt>
          <dd>{{ detail.trip.start_date }} 至 {{ detail.trip.end_date }}</dd>
          <dt>目的地</dt>
          <dd>{{ detail.trip.destination || '未填写' }}</dd>
          <dt>阶段</dt>
          <dd>
            {{ phaseLabel(detail.phase) }} · {{ detail.trip.archived_at ? '已归档' : '未归档' }}
          </dd>
          <dt>旅行时区</dt>
          <dd>{{ detail.trip.timezone }}</dd>
          <dt>预算</dt>
          <dd>
            {{
              detail.trip.budget_amount === null
                ? '未设置'
                : `${detail.trip.budget_amount} ${detail.trip.currency_code}`
            }}
          </dd>
          <dt>短途路线</dt>
          <dd>
            {{ detail.trip.route_short_mode === 'cycling' ? '骑行' : '步行' }} ·
            {{ detail.trip.route_short_distance_meters }} 米以内
          </dd>
          <dt>备注</dt>
          <dd class="preserve-text">{{ detail.trip.notes || '未填写' }}</dd>
          <dt>最近更新</dt>
          <dd>{{ formatTime(detail.trip.updated_at) }}</dd>
        </dl>
      </section>
      <section class="trip-content-section" aria-label="旅行内容">
        <el-tabs v-model="kind" @tab-change="changeKind"
          ><el-tab-pane name="itinerary" label="行程" /><el-tab-pane
            name="packing"
            label="物资" /><el-tab-pane name="todos" label="待办" /><el-tab-pane
            name="members"
            label="成员"
        /></el-tabs>
        <div v-if="contentError" class="form-error" role="alert">
          {{ contentError }}<el-button text @click="refreshContent">重试</el-button>
        </div>
        <div v-if="contentLoading" class="empty-state" role="status">正在加载内容…</div>
        <template v-if="content">
          <TripContentList :content="content" />
          <div class="table-footer">
            <span>共 {{ content.total }} 项</span
            ><el-pagination
              :current-page="page"
              :page-size="20"
              :total="content.total"
              layout="prev, pager, next"
              :pager-count="5"
              @current-change="changePage"
            />
          </div>
        </template>
      </section>
    </template>
  </section>
</template>
