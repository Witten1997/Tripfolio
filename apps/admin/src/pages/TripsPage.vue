<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Search, RefreshCw } from '@lucide/vue'
import { listTrips, type AdminTripPage } from '../api'
import { phaseLabel } from '../format'

const route = useRoute()
const router = useRouter()
const filters = reactive({
  account_id: '',
  q: '',
  phase: '',
  archived: 'all',
  trash: 'exclude',
  date_from: '',
  date_to: '',
})
const result = ref<AdminTripPage | null>(null)
const loading = ref(false)
const errorMessage = ref('')
const page = ref(1)
let request = 0
async function refresh() {
  const current = ++request
  loading.value = true
  errorMessage.value = ''
  result.value = null
  try {
    const response = await listTrips({ ...filters, page: String(page.value), page_size: '20' })
    if (current === request) result.value = response
  } catch (error) {
    if (current === request)
      errorMessage.value = error instanceof Error ? error.message : '旅行加载失败。'
  } finally {
    if (current === request) loading.value = false
  }
}
async function search(value = 1) {
  const query = { ...filters, page: String(value) }
  if (router.resolve({ name: 'trips', query }).fullPath === route.fullPath) await refresh()
  else await router.replace({ name: 'trips', query })
}
watch(
  () => route.fullPath,
  () => {
    if (route.name !== 'trips') return
    const defaults = {
      account_id: '',
      q: '',
      phase: '',
      archived: 'all',
      trash: 'exclude',
      date_from: '',
      date_to: '',
    }
    for (const key of Object.keys(defaults) as (keyof typeof filters)[])
      filters[key] = typeof route.query[key] === 'string' ? route.query[key] : defaults[key]
    page.value = Number(route.query.page || 1)
    void refresh()
  },
  { immediate: true },
)
</script>

<template>
  <section class="account-page" :aria-busy="loading">
    <div class="page-heading">
      <div>
        <p class="eyebrow">PLATFORM TRIPS</p>
        <h1>旅行管理</h1>
        <p class="muted">按所属用户定位旅行，查看行程与准备事项。</p>
      </div>
      <el-button :loading="loading" @click="refresh"
        ><RefreshCw v-if="!loading" :size="15" />刷新</el-button
      >
    </div>
    <form class="user-filters" @submit.prevent="search()">
      <div class="search-field">
        <label for="trip-search" class="small-label">旅行名称</label
        ><el-input
          id="trip-search"
          v-model="filters.q"
          placeholder="搜索旅行名称"
          :maxlength="120"
          clearable
        />
      </div>
      <div class="search-field">
        <label for="trip-owner" class="small-label">所属账号编号</label
        ><el-input
          id="trip-owner"
          v-model="filters.account_id"
          placeholder="可从用户详情进入"
          clearable
        />
      </div>
      <div class="status-field">
        <label for="trip-phase" class="small-label">旅行阶段</label
        ><el-select id="trip-phase" v-model="filters.phase" placeholder="全部阶段"
          ><el-option label="全部阶段" value="" /><el-option
            label="计划中"
            value="planned" /><el-option label="进行中" value="ongoing" /><el-option
            label="已结束"
            value="ended"
        /></el-select>
      </div>
      <div class="status-field">
        <label for="trip-archived" class="small-label">归档状态</label
        ><el-select id="trip-archived" v-model="filters.archived"
          ><el-option label="全部" value="all" /><el-option label="已归档" value="true" /><el-option
            label="未归档"
            value="false"
        /></el-select>
      </div>
      <div class="status-field">
        <label for="trip-trash" class="small-label">回收站</label
        ><el-select id="trip-trash" v-model="filters.trash"
          ><el-option label="不含回收站" value="exclude" /><el-option
            label="仅回收站"
            value="only" /><el-option label="全部" value="all"
        /></el-select>
      </div>
      <div class="date-field">
        <label for="trip-from" class="small-label">旅行日期起</label
        ><input
          id="trip-from"
          type="date"
          v-model="filters.date_from"
          :max="filters.date_to || undefined"
        />
      </div>
      <div class="date-field">
        <label for="trip-to" class="small-label">旅行日期止</label
        ><input
          id="trip-to"
          type="date"
          v-model="filters.date_to"
          :min="filters.date_from || undefined"
        />
      </div>
      <el-button native-type="submit" type="primary" :loading="loading"
        ><Search v-if="!loading" :size="15" />查询</el-button
      >
    </form>
    <div v-if="errorMessage" class="form-error" role="alert">
      {{ errorMessage }}<el-button text @click="refresh">重试</el-button>
    </div>
    <div class="table-panel">
      <el-table
        :data="result?.data || []"
        v-loading="loading"
        empty-text="没有符合条件的旅行"
        row-key="id"
      >
        <el-table-column label="旅行" min-width="210"
          ><template #default="{ row }"
            ><RouterLink class="user-name-button" :to="{ name: 'trip', params: { id: row.id } }">{{
              row.name
            }}</RouterLink>
            <p class="user-email">{{ row.destination || '未填写目的地' }}</p></template
          ></el-table-column
        >
        <el-table-column label="所属用户" min-width="210"
          ><template #default="{ row }"
            >{{ row.owner.nickname }}
            <p class="user-email">{{ row.owner.email }}</p></template
          ></el-table-column
        >
        <el-table-column label="日期" min-width="205"
          ><template #default="{ row }"
            >{{ row.start_date }} 至 {{ row.end_date }}</template
          ></el-table-column
        >
        <el-table-column label="阶段与状态" min-width="180"
          ><template #default="{ row }"
            ><span class="status-pill normal">{{ phaseLabel(row.phase) }}</span>
            <span v-if="row.archived_at" class="status-pill muted-status">已归档</span>
            <span v-if="row.deleted_at" class="status-pill muted-status">回收站</span></template
          ></el-table-column
        >
        <el-table-column label="操作" width="95" fixed="right"
          ><template #default="{ row }"
            ><RouterLink class="text-link" :to="{ name: 'trip', params: { id: row.id } }"
              >查看详情</RouterLink
            ></template
          ></el-table-column
        >
      </el-table>
      <div class="table-footer">
        <span>共 {{ result?.total ?? 0 }} 趟旅行</span
        ><el-pagination
          :current-page="page"
          :page-size="20"
          :total="result?.total ?? 0"
          layout="prev, pager, next"
          :pager-count="5"
          @current-change="search"
        />
      </div>
    </div>
  </section>
</template>
