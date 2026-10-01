<script setup lang="ts">
import {
  ArrowUpRight,
  CalendarDays,
  Footprints,
  Info,
  List,
  Luggage,
  Map,
  MapPin,
  SlidersHorizontal,
  Wallet,
} from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import DashboardMap from '@/desktop/components/DashboardMap.vue'
import ResponsiveEditorShell from '@/desktop/components/ResponsiveEditorShell.vue'
import type { DashboardQuery, DashboardTrip } from '@/shared/api/dashboard'
import { useMetadataStore } from '@/shared/stores/metadata'
import {
  averageMoney,
  coveredDays,
  spendingCategories,
  tripNet,
  uniquePlaces,
  visitedCities,
  type SpendingScope,
} from '@/shared/travel/dashboardView'
import { itineraryKindLabels } from '@/shared/travel/itineraryKinds'
import { formatMoney, sumMoney } from '@/shared/travel/statisticsView'
import { useDashboard } from '@/shared/travel/useDashboard'

const metadata = useMetadataStore()
const query = ref<DashboardQuery>({})
const { snapshot, loading, error, saving, writeError, notice, load, togglePlace } =
  useDashboard(query)
const period = ref('all')
const dateFrom = ref('')
const dateTo = ref('')
const dateError = ref('')
const scope = ref<SpendingScope>('personal')
const currency = ref('CNY')
const mapView = ref<'map' | 'list'>('map')
const selectedCityCode = ref('')
const dialog = ref<'rules' | 'places' | null>(null)
const search = ref('')
const managementTrip = ref('')
const visibleTrips = ref(8)

const trips = computed(() => snapshot.value?.trips ?? [])
const places = computed(() => snapshot.value?.places ?? [])
const cities = computed(() => visitedCities(places.value))
const provinceCount = computed(() => new Set(cities.value.map((city) => city.provinceCode)).size)
const placeCount = computed(() => uniquePlaces(cities.value.flatMap((city) => city.records)).length)
const selectedCity = computed(() =>
  cities.value.find((city) => city.code === selectedCityCode.value),
)
const years = computed(() =>
  [...new Set([new Date().getFullYear(), ...(snapshot.value?.years ?? [])])].sort((a, b) => b - a),
)
const periodLabel = computed(() =>
  !query.value.date_from
    ? '全部旅行'
    : query.value.date_from.endsWith('-01-01') &&
        query.value.date_to === `${query.value.date_from.slice(0, 4)}-12-31`
      ? `${query.value.date_from.slice(0, 4)} 年`
      : `${query.value.date_from} 至 ${query.value.date_to}`,
)
const currencies = computed(() =>
  [...new Set(trips.value.map((trip) => trip.currency_code))].sort(),
)
const currencyTrips = computed(() =>
  trips.value.filter((trip) => trip.currency_code === currency.value),
)
const categories = computed(() => spendingCategories(currencyTrips.value, scope.value))
const totalNet = computed(() => sumMoney(categories.value.map((row) => row.net)))
const totalExpense = computed(() => sumMoney(categories.value.map((row) => row.expense)))
const totalRefund = computed(() => sumMoney(categories.value.map((row) => row.refund)))
const currencyDays = computed(() => coveredDays(currencyTrips.value))
const perTrip = computed(() =>
  averageMoney(totalNet.value, currencyTrips.value.length, metadata.minorUnits(currency.value)),
)
const perDay = computed(() =>
  averageMoney(totalNet.value, currencyDays.value, metadata.minorUnits(currency.value)),
)
const ratioAvailable = computed(
  () => Number(totalNet.value) > 0 && categories.value.every((row) => Number(row.net) >= 0),
)
const slices = computed(() => {
  let offset = 0
  return categories.value.map((row, index) => {
    const share = ratioAvailable.value ? Number(row.net) / Number(totalNet.value) : 0
    const slice = { ...row, share, offset, color: `var(--tf-chart-${(index % 6) + 1})` }
    offset += share * 100
    return slice
  })
})
const monthly = computed(() => snapshot.value?.monthly_days ?? Array<number>(12).fill(0))
const maxMonthDays = computed(() => Math.max(1, ...monthly.value))
const tripNames = computed(
  () => new globalThis.Map(trips.value.map((trip) => [trip.id, trip.name])),
)
const managedPlaces = computed(() =>
  places.value.filter(
    (place) =>
      (!managementTrip.value || place.trip_id === managementTrip.value) &&
      `${place.name} ${place.address} ${place.region?.city_name ?? ''}`
        .toLowerCase()
        .includes(search.value.trim().toLowerCase()),
  ),
)
const excludedCount = computed(
  () => places.value.filter((place) => place.excluded && place.kind !== 'transport').length,
)

function money(value: string) {
  if (value === '—') return value
  const [integer, fraction = ''] = value.split('.')
  const units = metadata.minorUnits(currency.value)
  return formatMoney(units ? `${integer}.${fraction.padEnd(units, '0')}` : value)
}
function tripCities(trip: DashboardTrip) {
  return (
    cities.value
      .filter((city) => city.records.some((place) => place.trip_id === trip.id))
      .map((city) => city.name)
      .join('、') || '暂无已识别足迹'
  )
}
function applyDates() {
  dateError.value = ''
  if (!dateFrom.value || !dateTo.value) {
    dateError.value = '请选择完整的起止日期。'
    return
  }
  if (dateFrom.value > dateTo.value) {
    dateError.value = '结束日期不能早于开始日期。'
    return
  }
  query.value = { date_from: dateFrom.value, date_to: dateTo.value }
}
function closeDialog() {
  if (!saving.value) dialog.value = null
}

watch(period, (value) => {
  dateError.value = ''
  if (value === 'custom') return
  query.value = value === 'all' ? {} : { date_from: `${value}-01-01`, date_to: `${value}-12-31` }
})
watch(cities, (value) => {
  if (!value.some((city) => city.code === selectedCityCode.value))
    selectedCityCode.value = value[0]?.code ?? ''
})
watch(currencies, (value) => {
  if (!value.includes(currency.value))
    currency.value = value.includes('CNY') ? 'CNY' : (value[0] ?? 'CNY')
})
watch(query, () => {
  visibleTrips.value = 8
  managementTrip.value = ''
  search.value = ''
})
</script>

<template>
  <div class="dashboard">
    <header class="dashboard-header">
      <div>
        <p class="eyebrow">属于你的旅行记忆</p>
        <h1>旅行看板</h1>
        <p class="subtitle">把走过的路，变成看得见的积累。</p>
      </div>
      <div class="header-actions">
        <button class="button quiet" @click="dialog = 'rules'"><Info :size="16" />统计口径</button>
        <label class="period-control"
          ><CalendarDays :size="17" /><select
            v-model="period"
            aria-label="旅行统计时间"
            :disabled="!!saving"
          >
            <option value="all">全部时间</option>
            <option v-for="year in years" :key="year" :value="String(year)">{{ year }} 年</option>
            <option value="custom">自定义时间</option>
          </select></label
        >
      </div>
    </header>
    <form v-if="period === 'custom'" class="custom-range" @submit.prevent="applyDates">
      <span>按旅行出发日期</span
      ><label>开始<input v-model="dateFrom" type="date" required :disabled="!!saving" /></label
      ><label>结束<input v-model="dateTo" type="date" required :disabled="!!saving" /></label
      ><button class="button" :disabled="!!saving">应用</button
      ><span v-if="dateError" role="alert" class="error-text">{{ dateError }}</span>
    </form>
    <div v-if="error" class="feedback error-text" role="alert">
      <span>{{ error }}</span
      ><button class="button" :disabled="loading || !!saving" @click="load()">重新加载</button>
    </div>
    <div v-if="!snapshot && loading" class="loading-state" role="status">
      <span class="loader"></span>正在整理你的旅行记忆…
    </div>
    <template v-if="snapshot">
      <div class="scope-line">
        <span>{{ periodLabel }} · 仅统计已结束的旅行</span
        ><span v-if="loading" role="status">正在更新…</span
        ><button v-else class="text-button" :disabled="!!saving" @click="load()">刷新数据</button>
      </div>
      <section class="metrics" aria-label="旅行数据概览">
        <article class="metric">
          <Luggage />
          <p>旅行次数</p>
          <strong>{{ trips.length }}<small>次</small></strong
          ><span>每一次出发，都有意义</span>
        </article>
        <article class="metric">
          <MapPin />
          <p>去过的城市</p>
          <strong>{{ cities.length }}<small>座</small></strong
          ><span>跨越 {{ provinceCount }} 个省级地区</span>
        </article>
        <article class="metric">
          <Footprints />
          <p>留下的足迹</p>
          <strong>{{ placeCount }}<small>处</small></strong
          ><span>同一地点只计一次</span>
        </article>
        <article class="metric">
          <CalendarDays />
          <p>累计游玩</p>
          <strong>{{ snapshot.days }}<small>天</small></strong
          ><span>重叠日期已去重</span>
        </article>
        <article class="metric spending-metric">
          <Wallet />
          <p>{{ scope === 'personal' ? '我的旅行花销' : '整趟旅行花销' }}</p>
          <strong class="money-number">{{ money(totalNet) }}</strong
          ><span>{{ currency }} · 支出减退款</span>
        </article>
      </section>
      <div v-if="!trips.length" class="empty-state panel">
        <Luggage :size="38" />
        <h2>这段时间，还没有结束的旅行</h2>
        <p>旅行结束后，足迹、花销和游玩天数会汇集在这里。</p>
        <RouterLink class="button primary" :to="{ name: 'trips' }"
          >查看我的旅行<ArrowUpRight :size="16"
        /></RouterLink>
      </div>
      <template v-else>
        <section class="panel footprint-panel" aria-labelledby="footprints-title">
          <div class="panel-heading">
            <div>
              <h2 id="footprints-title">我的旅行足迹</h2>
              <p>
                {{ provinceCount }} 个省级地区，{{ cities.length }} 座城市，{{ placeCount }}
                处旅行记忆
              </p>
            </div>
            <button class="button" @click="dialog = 'places'">
              <SlidersHorizontal :size="15" />管理足迹
            </button>
          </div>
          <div
            v-if="snapshot.unresolved_places || snapshot.missing_coordinates"
            class="location-notice"
            role="status"
          >
            <Info :size="16" /><span
              >足迹尚未完整：<template v-if="snapshot.unresolved_places"
                >{{ snapshot.unresolved_places }} 个地点待识别国内省市；</template
              ><template v-if="snapshot.missing_coordinates"
                >{{ snapshot.missing_coordinates }} 个地点缺少坐标；</template
              >可在行程中补充地点。境外地点不计入国内地图。</span
            ><button
              v-if="snapshot.unresolved_places"
              class="text-button"
              :disabled="loading || !!saving"
              @click="load()"
            >
              {{ snapshot.next_location_after ? '继续识别' : '重试识别' }}
            </button>
          </div>
          <div class="map-layout">
            <div class="map-content">
              <div class="map-toolbar">
                <span class="section-kicker">CHINA · 国内足迹</span>
                <div class="segmented" aria-label="足迹展示方式">
                  <button :aria-pressed="mapView === 'map'" @click="mapView = 'map'">
                    <Map :size="15" />地图</button
                  ><button :aria-pressed="mapView === 'list'" @click="mapView = 'list'">
                    <List :size="15" />城市列表
                  </button>
                </div>
              </div>
              <DashboardMap
                v-if="mapView === 'map'"
                :cities="cities"
                :selected="selectedCityCode"
                @select="selectedCityCode = $event"
              />
              <div v-else class="city-grid">
                <button
                  v-for="city in cities"
                  :key="city.code"
                  :aria-pressed="selectedCityCode === city.code"
                  @click="selectedCityCode = city.code"
                >
                  <strong>{{ city.name }}</strong
                  ><span>{{ city.province }}</span
                  ><small>{{ city.visits }} 次到访 · {{ city.places.length }} 处地点</small>
                </button>
                <p v-if="!cities.length" class="empty-inline">暂无已识别的国内城市足迹。</p>
              </div>
            </div>
            <aside class="city-detail" aria-label="城市足迹详情">
              <template v-if="selectedCity">
                <label class="city-select"
                  >探索一座城市<select v-model="selectedCityCode" aria-label="选择足迹城市">
                    <option v-for="city in cities" :key="city.code" :value="city.code">
                      {{ city.name }} · {{ city.province }}
                    </option>
                  </select></label
                >
                <div class="city-heading">
                  <h3>{{ selectedCity.name }}</h3>
                  <span>{{ selectedCity.province }}</span>
                </div>
                <div class="city-facts">
                  <div>
                    <strong>{{ selectedCity.visits }}</strong
                    ><span>次到访</span>
                  </div>
                  <div>
                    <strong>{{ selectedCity.places.length }}</strong
                    ><span>处地点</span>
                  </div>
                </div>
                <ul class="place-list">
                  <li v-for="place in selectedCity.places" :key="place.id">
                    <MapPin :size="15" />
                    <div>
                      <RouterLink
                        :to="{ name: 'trip-itinerary', params: { tripId: place.trip_id } }"
                        >{{ place.name }}<ArrowUpRight :size="12" /></RouterLink
                      ><small
                        >{{ place.scheduled_on }} · {{ itineraryKindLabels[place.kind] }}</small
                      >
                    </div>
                  </li>
                </ul>
              </template>
              <div v-else class="empty-inline">
                <Footprints :size="30" />
                <h3>足迹从一个地点开始</h3>
                <p>为已结束旅行的行程添加地点坐标，即可在这里留下足迹。</p>
              </div>
            </aside>
          </div>
          <p class="panel-note">
            <Info
              :size="14"
            />已结束旅行自动归集；交通项目不作为到访凭据，没去的地点可手动排除。<span
              v-if="excludedCount"
              >已排除 {{ excludedCount }} 条。</span
            >
          </p>
        </section>
        <div class="analysis-grid">
          <section class="panel expense-panel" aria-labelledby="expenses-title">
            <div class="panel-heading">
              <div>
                <h2 id="expenses-title">旅行花销</h2>
                <p>看看每一笔钱，花在了哪里</p>
              </div>
              <div class="segmented" aria-label="花销统计范围">
                <button :aria-pressed="scope === 'personal'" @click="scope = 'personal'">
                  我的</button
                ><button :aria-pressed="scope === 'whole'" @click="scope = 'whole'">
                  整趟旅行
                </button>
              </div>
            </div>
            <div class="expense-meta">
              <label
                >记账币种<select v-model="currency" aria-label="花销币种">
                  <option v-for="code in currencies" :key="code" :value="code">{{ code }}</option>
                </select></label
              ><span>仅影响费用 · {{ currencyTrips.length }} 趟旅行</span>
            </div>
            <div class="expense-body">
              <div class="donut-wrap">
                <svg
                  v-if="ratioAvailable"
                  viewBox="0 0 160 160"
                  class="donut"
                  role="img"
                  aria-label="各分类净支出占比，具体金额见旁边明细"
                >
                  <circle
                    v-for="slice in slices"
                    :key="slice.id"
                    cx="80"
                    cy="80"
                    r="66"
                    fill="none"
                    :stroke="slice.color"
                    stroke-width="16"
                    pathLength="100"
                    :stroke-dasharray="`${slice.share * 100} ${100 - slice.share * 100}`"
                    :stroke-dashoffset="-slice.offset"
                    transform="rotate(-90 80 80)"
                  >
                    <title>{{ slice.name }}：{{ money(slice.net) }} {{ currency }}</title>
                  </circle>
                </svg>
                <div class="donut-center" :class="{ 'no-ring': !ratioAvailable }">
                  <span>净支出 · {{ currency }}</span
                  ><strong>{{ money(totalNet) }}</strong
                  ><small v-if="!ratioAvailable">{{
                    categories.length ? '退款已抵扣' : '暂无账目'
                  }}</small>
                </div>
              </div>
              <div class="category-list">
                <div v-for="slice in slices" :key="slice.id" class="category-row">
                  <span class="category-name"
                    ><i :style="{ background: slice.color }"></i>{{ slice.name }}</span
                  ><strong>{{ money(slice.net) }}</strong
                  ><small>{{
                    ratioAvailable ? `${(slice.share * 100).toFixed(1)}%` : '净额'
                  }}</small>
                </div>
                <p v-if="!categories.length" class="empty-inline">
                  记下旅途中的花销，<br />这里会为你汇总每一笔收支。
                </p>
              </div>
            </div>
            <p v-if="categories.length && !ratioAvailable" class="panel-note">
              总净额非正或存在负净额分类，按金额展示，不计算饼图占比。
            </p>
            <div class="expense-totals">
              <span>支出 {{ money(totalExpense) }}</span
              ><span>退款 −{{ money(totalRefund) }}</span>
            </div>
            <div class="averages">
              <div>
                <span>平均每次旅行</span
                ><strong
                  >{{ money(perTrip) }}<small>{{ currency }}</small></strong
                >
              </div>
              <div>
                <span>平均每天</span
                ><strong
                  >{{ money(perDay) }}<small>{{ currency }}</small></strong
                >
              </div>
            </div>
            <p class="panel-note">
              日均按这 {{ currencyTrips.length }} 趟旅行的
              {{ currencyDays }} 个不重复游玩日计算，包含行前与旅后账目。
            </p>
          </section>
          <section class="panel days-panel" aria-labelledby="days-title">
            <div class="panel-heading">
              <div>
                <h2 id="days-title">把日子交给旅行</h2>
                <p>每个月，都有值得出发的理由</p>
              </div>
              <CalendarDays class="heading-icon" />
            </div>
            <div class="days-total">
              <strong>{{ snapshot.days }}</strong
              ><span>天在路上</span>
            </div>
            <div
              class="month-chart"
              role="img"
              :aria-label="monthly.map((days, index) => `${index + 1}月${days}天`).join('，')"
            >
              <div v-for="(days, index) in monthly" :key="index" class="month-column">
                <div class="bar-space">
                  <span v-if="days">{{ days }}</span
                  ><i
                    :style="{
                      height: days ? `${Math.max(3, (days / maxMonthDays) * 132)}px` : '3px',
                    }"
                    :class="{ active: days > 0 }"
                  ></i>
                </div>
                <small>{{ index + 1 }}月</small>
              </div>
            </div>
            <p class="panel-note">
              {{
                period === 'all' || period === 'custom' ? '各年同月合并；' : ''
              }}按旅行实际覆盖的自然日统计，跨月分别计入，重叠日期去重。
            </p>
          </section>
        </div>
        <section class="panel trips-panel" aria-labelledby="trips-title">
          <div class="panel-heading">
            <div>
              <h2 id="trips-title">每一次出发</h2>
              <p>构成这份旅行看板的 {{ trips.length }} 趟旅程</p>
            </div>
            <RouterLink class="text-button" :to="{ name: 'trips' }"
              >全部旅行<ArrowUpRight :size="15"
            /></RouterLink>
          </div>
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>旅行</th>
                  <th>旅行日期</th>
                  <th>城市足迹</th>
                  <th>天数</th>
                  <th>{{ scope === 'personal' ? '我的净支出' : '整趟净支出' }}</th>
                  <th><span class="sr-only">查看</span></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="trip in trips.slice(0, visibleTrips)" :key="trip.id">
                  <td>
                    <RouterLink
                      class="trip-name"
                      :to="{ name: 'trip-itinerary', params: { tripId: trip.id } }"
                      >{{ trip.name }}</RouterLink
                    >
                  </td>
                  <td class="date-cell">{{ trip.start_date }}<br />{{ trip.end_date }}</td>
                  <td>{{ tripCities(trip) }}</td>
                  <td class="nowrap">{{ trip.days }} 天</td>
                  <td class="nowrap">
                    <RouterLink :to="{ name: 'trip-ledger', params: { tripId: trip.id } }"
                      >{{ formatMoney(tripNet(trip, scope))
                      }}<small class="currency-code">{{ trip.currency_code }}</small></RouterLink
                    >
                  </td>
                  <td>
                    <RouterLink
                      :to="{ name: 'trip-itinerary', params: { tripId: trip.id } }"
                      :aria-label="`查看${trip.name}`"
                      ><ArrowUpRight :size="18"
                    /></RouterLink>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <button
            v-if="visibleTrips < trips.length"
            class="button more-trips"
            @click="visibleTrips += 12"
          >
            加载更多旅行（还有 {{ trips.length - visibleTrips }} 趟）
          </button>
        </section>
      </template>
    </template>
    <ResponsiveEditorShell
      :model-value="dialog !== null"
      :title="dialog === 'places' ? '管理旅行足迹' : '看板统计口径'"
      :before-close="closeDialog"
    >
      <div v-if="dialog === 'rules'" class="rules-content">
        <section>
          <h3>哪些旅行会计入？</h3>
          <p>
            按旅行时区判断，仅统计结束日期早于今天的旅行；归档旅行保留，回收站旅行排除。时间筛选以旅行出发日期为准。
          </p>
        </section>
        <section>
          <h3>怎样算去过？</h3>
          <p>
            已结束旅行中的游玩、住宿、餐饮及其他非交通地点自动归集国内足迹。交通项目不作为到访凭据，仅在机场、车站中转不计入。没去的地点可在「管理足迹」中排除。
          </p>
          <p>
            省份、城市和地点分别去重；有地图地点编号时优先按编号识别，否则按地点名称和坐标识别。缺少坐标或未识别国内省市的地点暂不计入，境外地点不计入国内地图。
          </p>
        </section>
        <section>
          <h3>游玩天数怎么算？</h3>
          <p>
            开始、结束两天均计入；多趟旅行覆盖同一天时只算一天。月度图按实际覆盖日期分配到各月，跨年筛选会保留整趟旅行，同月份可跨年合并。
          </p>
        </section>
        <section>
          <h3>花销包含什么？</h3>
          <p>
            默认统计个人承担的净支出，可切换整趟费用。净支出 = 支出 −
            退款，包含选中旅行的行前和旅后账目。不同币种分别统计，不做换算或合并。
          </p>
          <p>
            费用币种只影响花销区域与顶部花销卡片。日均使用同币种旅行的不重复游玩日；每次均值使用同币种旅行次数。每趟旅行记录始终显示自己的币种。
          </p>
        </section>
      </div>
      <div v-else class="management">
        <p>
          默认计入非交通地点。关闭没去的地点后，该记录不再贡献足迹；同一地点的其他到访记录仍会保留。旅行次数、天数和账目不受影响。
        </p>
        <div class="management-filters">
          <input v-model="search" placeholder="搜索地点、城市或地址" aria-label="搜索足迹" /><select
            v-model="managementTrip"
            aria-label="筛选足迹所属旅行"
          >
            <option value="">全部旅行</option>
            <option v-for="trip in trips" :key="trip.id" :value="trip.id">{{ trip.name }}</option>
          </select>
        </div>
        <p v-if="writeError" role="alert" class="error-text">{{ writeError }}</p>
        <p v-if="notice" role="status">{{ notice }}</p>
        <p v-if="loading" role="status">正在刷新足迹…</p>
        <ul class="management-list">
          <li v-for="place in managedPlaces" :key="place.id">
            <div>
              <strong>{{ place.name }}</strong>
              <p>{{ tripNames.get(place.trip_id) }} · {{ place.scheduled_on }}</p>
              <small>{{
                place.kind === 'transport'
                  ? '交通项目 · 不计入足迹'
                  : place.excluded
                    ? '已手动排除'
                    : place.region
                      ? `${place.region.province_name} · ${place.region.city_name}`
                      : place.latitude === null || place.longitude === null
                        ? '缺少坐标 · 暂不计入'
                        : '国内省市待识别 · 暂不计入'
              }}</small
              ><RouterLink
                v-if="!place.region && place.kind !== 'transport'"
                :to="{ name: 'trip-itinerary', params: { tripId: place.trip_id } }"
                @click="closeDialog"
                >前往完善地点</RouterLink
              >
            </div>
            <button
              v-if="place.kind !== 'transport'"
              class="footprint-switch"
              role="switch"
              :aria-checked="!place.excluded"
              :aria-label="`${place.name}是否计入足迹`"
              :disabled="!!saving || loading"
              @click="togglePlace(place)"
            >
              <span></span
              ><i class="sr-only">{{
                saving === place.id ? '保存中' : place.excluded ? '已排除' : '计入'
              }}</i>
            </button>
          </li>
        </ul>
        <p v-if="!managedPlaces.length" class="empty-inline">没有符合条件的地点。</p>
      </div>
      <template #footer
        ><button class="button" :disabled="!!saving" @click="closeDialog">
          {{ saving ? '正在保存…' : '关闭' }}
        </button></template
      >
    </ResponsiveEditorShell>
  </div>
</template>

<style scoped>
.dashboard {
  max-width: 1540px;
  margin-inline: auto;
  color: var(--tf-text-1);
  font-size: 14px;
}
.dashboard *,
.management * {
  box-sizing: border-box;
}
h1,
h2,
h3,
p {
  margin: 0;
}
h1 {
  font-size: 30px;
  line-height: 1.3;
  font-weight: 700;
  letter-spacing: -0.6px;
}
h2 {
  font-size: 18px;
  font-weight: 650;
}
h3 {
  font-size: 17px;
}
button,
select,
input {
  font: inherit;
  color: inherit;
}
button,
select {
  cursor: pointer;
}
button:disabled,
select:disabled {
  opacity: 0.55;
  cursor: wait;
}
button:focus-visible,
a:focus-visible,
select:focus-visible,
input:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 3px;
}
a {
  color: var(--tf-accent);
  text-decoration: none;
}
a:hover {
  text-decoration: underline;
}
.dashboard-header,
.header-actions,
.panel-heading,
.map-toolbar,
.expense-meta,
.scope-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.dashboard-header {
  padding-block: 10px 22px;
}
.eyebrow {
  margin-bottom: 7px;
  color: var(--tf-accent);
  font-size: 12px;
  letter-spacing: 1.5px;
}
.subtitle {
  margin-top: 8px;
  color: var(--tf-text-3);
}
.header-actions {
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 10px;
}
.button,
.period-control {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 7px;
  min-height: 38px;
  padding: 8px 13px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-raised);
  color: var(--tf-text-2);
  white-space: nowrap;
}
.button:hover {
  background: var(--tf-accent-soft);
  color: var(--tf-accent);
}
.button.quiet {
  background: transparent;
}
.button.primary {
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
}
.period-control select {
  border: 0;
  background: transparent;
  outline-offset: 5px;
}
.text-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  min-height: 32px;
  border: 0;
  padding: 3px 5px;
  background: transparent;
  color: var(--tf-accent);
  font-size: 12px;
  white-space: nowrap;
}
.custom-range {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 16px;
  color: var(--tf-text-2);
}
.custom-range label {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
input,
.city-select select,
.expense-meta select,
.management select {
  min-height: 38px;
  min-width: 0;
  padding: 7px 10px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-raised);
}
.scope-line {
  margin-bottom: 13px;
  color: var(--tf-text-3);
  font-size: 12px;
}
.metrics {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 24px;
}
.metric {
  position: relative;
  min-width: 0;
  padding: 22px 18px;
  border-radius: var(--tf-radius-card);
  background: var(--tf-surface-raised);
  border: 1px solid var(--tf-line-soft);
}
.metric > svg {
  position: absolute;
  inset-block-start: 20px;
  inset-inline-end: 18px;
  width: 18px;
  height: 18px;
  color: var(--tf-accent);
  opacity: 0.85;
}
.metric p {
  padding-inline-end: 20px;
  margin-bottom: 14px;
  color: var(--tf-text-2);
  font-size: 13px;
}
.metric strong {
  display: block;
  font-size: 33px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.7px;
  line-height: 1.2;
  overflow-wrap: anywhere;
}
.metric strong small {
  margin-inline-start: 5px;
  font-size: 12px;
  color: var(--tf-text-3);
  font-weight: 400;
}
.metric > span {
  display: block;
  margin-top: 12px;
  color: var(--tf-text-3);
  font-size: 11px;
}
.metric.spending-metric {
  background: var(--tf-accent);
  color: var(--tf-accent-contrast);
}
.spending-metric > svg,
.spending-metric p,
.spending-metric > span {
  color: var(--tf-accent-contrast);
}
.metric .money-number {
  font-size: clamp(21px, 2.1vw, 30px);
}
.panel {
  min-width: 0;
  padding: 24px;
  border-radius: var(--tf-radius-panel);
  background: var(--tf-surface-raised);
  border: 1px solid var(--tf-line-soft);
}
.panel-heading {
  flex-wrap: wrap;
  margin-bottom: 22px;
}
.panel-heading p {
  margin-top: 7px;
  color: var(--tf-text-3);
  font-size: 12px;
}
.panel-note {
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 18px;
  color: var(--tf-text-3);
  font-size: 11px;
  line-height: 1.7;
}
.panel-note svg {
  flex: 0 0 auto;
  margin-top: 2px;
}
.map-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 260px;
  gap: 22px;
}
.map-content {
  min-width: 0;
}
.map-toolbar {
  flex-wrap: wrap;
}
.section-kicker {
  font-size: 10px;
  letter-spacing: 1.8px;
  color: var(--tf-text-3);
}
.segmented {
  display: flex;
  padding: 3px;
  gap: 2px;
  border: 1px solid var(--tf-line-soft);
  border-radius: 10px;
  background: var(--tf-surface-sunken);
}
.segmented button {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  min-height: 30px;
  padding: 5px 10px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--tf-text-3);
  font-size: 12px;
}
.segmented button[aria-pressed='true'] {
  background: var(--tf-surface-raised);
  color: var(--tf-accent);
  box-shadow: var(--tf-shadow-1);
}
.city-detail {
  padding: 18px;
  border-radius: 16px;
  background: var(--tf-accent-soft);
  min-width: 0;
}
.city-select {
  display: grid;
  gap: 8px;
  color: var(--tf-text-3);
  font-size: 11px;
}
.city-select select {
  width: 100%;
  font-size: 12px;
  color: var(--tf-text-2);
}
.city-heading {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 10px;
  margin-top: 22px;
}
.city-heading h3 {
  font-size: 23px;
}
.city-heading > span {
  font-size: 12px;
  color: var(--tf-text-3);
}
.city-facts {
  display: grid;
  grid-template-columns: 1fr 1fr;
  margin-block: 18px 20px;
  gap: 12px;
}
.city-facts strong {
  font-size: 23px;
  font-variant-numeric: tabular-nums;
}
.city-facts span {
  display: block;
  margin-top: 4px;
  font-size: 11px;
  color: var(--tf-text-3);
}
.place-list {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-height: 205px;
  overflow: auto;
  list-style: none;
  margin: 0;
  padding: 0 4px 0 0;
}
.place-list li {
  display: flex;
  gap: 8px;
  align-items: flex-start;
}
.place-list li > svg {
  flex: 0 0 auto;
  margin-top: 3px;
  color: var(--tf-accent);
}
.place-list a {
  display: inline;
  color: var(--tf-text-1);
  line-height: 1.6;
  font-size: 13px;
}
.place-list a svg {
  display: inline-block;
  vertical-align: middle;
  margin-inline-start: 4px;
}
.place-list small {
  display: block;
  margin-top: 4px;
  color: var(--tf-text-3);
  font-size: 10px;
}
.city-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  align-content: start;
  gap: 12px;
  min-height: 320px;
  max-height: 465px;
  overflow: auto;
  margin-top: 20px;
}
.city-grid button {
  display: flex;
  align-items: flex-start;
  flex-direction: column;
  gap: 7px;
  padding: 16px;
  border-radius: 12px;
  border: 1px solid var(--tf-line-soft);
  background: var(--tf-surface-inset);
  text-align: start;
}
.city-grid button[aria-pressed='true'] {
  border-color: var(--tf-accent);
  background: var(--tf-accent-soft);
}
.city-grid span,
.city-grid small {
  color: var(--tf-text-3);
  font-size: 11px;
}
.analysis-grid {
  display: grid;
  grid-template-columns: 1.12fr 1fr;
  gap: 20px;
  margin-block: 24px;
}
.expense-meta {
  color: var(--tf-text-3);
  font-size: 11px;
  flex-wrap: wrap;
  gap: 8px;
}
.expense-meta label {
  display: flex;
  align-items: center;
  gap: 8px;
}
.expense-meta select {
  min-height: 30px;
  padding-block: 4px;
  font-size: 12px;
}
.expense-body {
  display: grid;
  grid-template-columns: 170px minmax(0, 1fr);
  gap: 24px;
  align-items: center;
  margin-block: 20px 18px;
}
.donut-wrap {
  position: relative;
  width: 170px;
  min-height: 170px;
}
.donut {
  display: block;
  width: 100%;
}
.donut-center {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  gap: 7px;
  padding: 20px;
  pointer-events: none;
}
.donut-center span,
.donut-center small {
  font-size: 10px;
  color: var(--tf-text-3);
}
.donut-center strong {
  max-width: 135px;
  font-size: 19px;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
  text-align: center;
}
.no-ring {
  border-radius: 50%;
  background: var(--tf-surface-sunken);
}
.category-list {
  max-height: 210px;
  overflow: auto;
}
.category-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto 38px;
  align-items: center;
  gap: 8px;
  margin-block: 13px;
  font-size: 12px;
}
.category-name {
  display: flex;
  align-items: center;
  gap: 7px;
  overflow-wrap: anywhere;
}
.category-name i {
  width: 7px;
  height: 7px;
  flex: 0 0 auto;
  border-radius: 50%;
}
.category-row strong {
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}
.category-row small {
  text-align: end;
  color: var(--tf-text-3);
  font-size: 10px;
}
.expense-totals {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--tf-line-soft);
  font-size: 12px;
  color: var(--tf-text-3);
}
.averages {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 20px;
  padding-top: 18px;
}
.averages span {
  color: var(--tf-text-3);
  font-size: 11px;
}
.averages strong {
  display: block;
  margin-top: 7px;
  font-size: 20px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
.averages small {
  font-size: 10px;
  font-weight: 400;
  margin-inline-start: 6px;
}
.heading-icon {
  color: var(--tf-accent);
  width: 20px;
  height: 20px;
}
.days-total {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-block: 28px 20px;
}
.days-total strong {
  font-size: 40px;
  font-weight: 600;
  letter-spacing: -1px;
  font-variant-numeric: tabular-nums;
}
.days-total span {
  color: var(--tf-text-3);
  font-size: 12px;
}
.month-chart {
  display: grid;
  grid-template-columns: repeat(12, minmax(0, 1fr));
  gap: 9px;
  margin-top: 25px;
}
.month-column {
  min-width: 0;
  text-align: center;
}
.bar-space {
  display: flex;
  height: 158px;
  align-items: center;
  justify-content: flex-end;
  flex-direction: column;
  gap: 7px;
}
.bar-space span {
  font-size: 10px;
  color: var(--tf-accent);
  font-variant-numeric: tabular-nums;
}
.bar-space i {
  display: block;
  width: 100%;
  max-width: 25px;
  border-radius: 5px 5px 2px 2px;
  background: var(--tf-line-soft);
}
.bar-space i.active {
  background: color-mix(in srgb, var(--tf-accent) 78%, var(--tf-surface-raised));
}
.month-column small {
  display: block;
  margin-top: 11px;
  color: var(--tf-text-3);
  font-size: 10px;
  white-space: nowrap;
}
.days-panel .panel-note {
  margin-top: 28px;
}
.table-scroll {
  position: relative;
  overflow: auto;
}
table {
  border-collapse: collapse;
  width: 100%;
  font-size: 12px;
  text-align: start;
}
th {
  padding: 12px 8px;
  font-weight: 400;
  font-size: 11px;
  color: var(--tf-text-3);
  border-bottom: 1px solid var(--tf-line-soft);
  text-align: start;
}
td {
  padding: 17px 8px;
  border-bottom: 1px solid var(--tf-line-soft);
  line-height: 1.6;
  max-width: 260px;
  overflow-wrap: anywhere;
}
tr:last-child td {
  border-bottom: 0;
}
td a {
  color: var(--tf-text-1);
}
.trip-name {
  font-weight: 600;
  font-size: 13px;
}
.date-cell {
  color: var(--tf-text-3);
  font-size: 11px;
  white-space: nowrap;
}
.nowrap {
  white-space: nowrap;
}
.currency-code {
  display: block;
  color: var(--tf-text-3);
  font-size: 10px;
}
.more-trips {
  display: flex;
  margin: 20px auto 0;
}
.empty-state,
.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  gap: 18px;
  padding: 70px 24px;
  text-align: center;
}
.empty-state > svg,
.empty-inline > svg {
  color: var(--tf-accent);
}
.empty-state p,
.empty-inline {
  color: var(--tf-text-3);
  line-height: 1.8;
  font-size: 12px;
}
.empty-inline {
  padding: 22px 4px;
}
.empty-inline h3 {
  margin-block: 14px 10px;
  font-size: 16px;
}
.feedback,
.location-notice {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px;
  margin-bottom: 16px;
  border-radius: 10px;
  background: var(--tf-warning-soft);
  font-size: 12px;
  line-height: 1.7;
}
.location-notice > svg {
  flex: 0 0 auto;
  color: var(--tf-warning);
}
.location-notice > span {
  flex: 1;
}
.error-text {
  color: var(--tf-danger);
}
.loader {
  width: 22px;
  height: 22px;
  border: 2px solid var(--tf-line-soft);
  border-top-color: var(--tf-accent);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
.rules-content,
.management {
  color: var(--tf-text-2);
  font-size: 13px;
  line-height: 1.9;
}
.rules-content section + section {
  margin-top: 24px;
}
.rules-content h3 {
  margin-bottom: 8px;
  color: var(--tf-text-1);
  font-size: 15px;
}
.rules-content p + p {
  margin-top: 8px;
}
.management-filters {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-block: 18px;
}
.management-filters input,
.management-filters select {
  width: 100%;
}
.management-list {
  margin: 14px 0 0;
  padding: 0;
  list-style: none;
}
.management-list li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  padding-block: 15px;
  border-bottom: 1px solid var(--tf-line-soft);
}
.management-list li > div {
  min-width: 0;
  overflow-wrap: anywhere;
}
.management-list strong {
  color: var(--tf-text-1);
}
.management-list p,
.management-list small {
  color: var(--tf-text-3);
  font-size: 11px;
}
.management-list a {
  display: block;
  font-size: 11px;
  color: var(--tf-accent);
}
.footprint-switch {
  position: relative;
  flex: 0 0 46px;
  width: 46px;
  height: 30px;
  border: 0;
  border-radius: 18px;
  padding: 3px;
  background: var(--tf-text-disabled);
  cursor: pointer;
}
.footprint-switch span {
  display: block;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: var(--tf-accent-contrast);
  box-shadow: var(--tf-shadow-1);
}
.footprint-switch[aria-checked='true'] {
  background: var(--tf-accent);
}
.footprint-switch[aria-checked='true'] span {
  transform: translateX(16px);
}
.footprint-switch:disabled {
  opacity: 0.55;
  cursor: wait;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
@media (max-width: 1230px) {
  .metrics {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
  .metric.spending-metric {
    grid-column: span 2;
  }
  .metric .money-number {
    font-size: 30px;
  }
  .analysis-grid {
    grid-template-columns: minmax(0, 1fr);
  }
  .expense-body {
    grid-template-columns: 200px minmax(0, 1fr);
  }
  .map-layout {
    grid-template-columns: minmax(0, 1fr) 225px;
    gap: 16px;
  }
  .city-detail {
    padding: 14px;
  }
}
@media (max-width: 960px) {
  .dashboard-header {
    align-items: flex-start;
  }
  .header-actions {
    flex-direction: column-reverse;
    align-items: flex-end;
  }
  .map-layout {
    grid-template-columns: minmax(0, 1fr);
  }
  .city-detail {
    padding: 18px;
  }
  .city-select {
    max-width: 320px;
  }
  .place-list {
    max-height: 210px;
  }
}
@media (max-width: 767px) {
  .dashboard {
    padding: 20px 16px 30px;
  }
  .dashboard-header {
    padding-block: 0 18px;
  }
  h1 {
    font-size: 26px;
  }
  .eyebrow {
    font-size: 10px;
  }
  .subtitle {
    font-size: 11px;
    max-width: 180px;
    line-height: 1.7;
  }
  .header-actions {
    gap: 4px;
  }
  .header-actions .button,
  .period-control {
    min-height: 36px;
    padding: 7px 9px;
    font-size: 11px;
  }
  .metrics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px;
    margin-bottom: 18px;
  }
  .metric {
    padding: 18px 15px;
  }
  .metric > svg {
    inset-block-start: 17px;
    inset-inline-end: 14px;
    width: 16px;
    height: 16px;
  }
  .metric p {
    font-size: 12px;
  }
  .metric strong {
    font-size: 29px;
  }
  .metric.spending-metric {
    grid-column: 1 / -1;
  }
  .metric.spending-metric strong {
    font-size: 32px;
  }
  .panel {
    padding: 18px 16px;
  }
  .panel-heading {
    gap: 12px;
    margin-bottom: 18px;
  }
  .panel-heading h2 {
    font-size: 17px;
  }
  .panel-heading p {
    font-size: 11px;
    line-height: 1.7;
  }
  .panel-heading .button {
    min-height: 34px;
    font-size: 11px;
    padding: 6px 9px;
  }
  .map-toolbar {
    gap: 10px;
  }
  .section-kicker {
    font-size: 9px;
    letter-spacing: 1px;
  }
  .location-notice {
    flex-wrap: wrap;
    font-size: 11px;
  }
  .location-notice > span {
    min-width: 180px;
  }
  .analysis-grid {
    gap: 18px;
    margin-block: 18px;
  }
  .expense-body {
    grid-template-columns: minmax(0, 1fr);
    gap: 8px;
  }
  .donut-wrap {
    justify-self: center;
    width: 180px;
    min-height: 180px;
  }
  .category-list {
    max-height: none;
    width: 100%;
  }
  .category-row {
    grid-template-columns: minmax(0, 1fr) auto 42px;
  }
  .month-chart {
    gap: 5px;
  }
  .averages {
    gap: 12px;
  }
  .averages strong {
    font-size: 19px;
  }
  .table-scroll {
    margin-inline: -4px;
  }
  table {
    min-width: 560px;
  }
  .trips-panel::after {
    display: block;
    content: '左右滑动查看完整记录';
    margin-top: 12px;
    font-size: 10px;
    color: var(--tf-text-3);
  }
  .management-filters {
    grid-template-columns: minmax(0, 1fr);
  }
  .custom-range {
    font-size: 12px;
  }
  .custom-range > span:first-child {
    width: 100%;
  }
}
@media (prefers-reduced-motion: reduce) {
  .loader {
    animation: none;
  }
}
</style>
