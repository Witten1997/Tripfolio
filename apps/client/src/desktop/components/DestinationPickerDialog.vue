<script setup lang="ts">
import { Search, X } from '@lucide/vue'
import {
  ElButton,
  ElDialog,
  ElEmpty,
  ElInput,
  ElSkeleton,
  ElTabPane,
  ElTabs,
  ElTag,
} from 'element-plus'
import { computed, nextTick, ref, watch } from 'vue'

import {
  isCityQueryReady,
  searchCities,
  type CityRecord,
  type CityScope,
} from '@/shared/cities/citySearch'

const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

const opened = ref(false)
const scope = ref<CityScope>('domestic')
const keyword = ref('')
const loading = ref(false)
const results = ref<CityRecord[]>([])
const selected = ref<string[]>([])
let generation = 0

const countryNames = new Intl.DisplayNames(['zh-CN'], { type: 'region' })
const prompt = computed(() =>
  isCityQueryReady(keyword.value) ? '没有找到匹配城市' : '输入城市名称开始搜索',
)

function parseDestinations(value: string) {
  return [
    ...new Set(
      value
        .split(/[、,，;；]/)
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  ]
}

function open() {
  selected.value = parseDestinations(props.modelValue)
  keyword.value = ''
  results.value = []
  scope.value = 'domestic'
  opened.value = true
}

function cityLabel(city: CityRecord) {
  return city[1]
}

function cityDetail(city: CityRecord) {
  const region = city[4]
  const country = countryNames.of(city[3]) ?? city[3]
  return [region, country]
    .filter((item, index, values) => item && values.indexOf(item) === index)
    .join(' · ')
}

function isSelected(city: CityRecord) {
  return selected.value.includes(cityLabel(city))
}

function toggle(city: CityRecord) {
  const label = cityLabel(city)
  selected.value = isSelected(city)
    ? selected.value.filter((item) => item !== label)
    : [...selected.value, label]
}

function remove(label: string) {
  selected.value = selected.value.filter((item) => item !== label)
}

function confirm() {
  emit('update:modelValue', selected.value.join('、'))
  opened.value = false
}

watch([keyword, scope], async () => {
  const request = ++generation
  const query = keyword.value.trim()
  results.value = []
  if (!isCityQueryReady(query)) {
    loading.value = false
    return
  }
  loading.value = true
  await nextTick()
  const cities = await searchCities(scope.value, query)
  if (request === generation) {
    results.value = cities
    loading.value = false
  }
})

defineExpose({ open })
</script>

<template>
  <ElDialog
    v-model="opened"
    title="选择目的地"
    width="min(600px, calc(100vw - 24px))"
    class="destination-picker-dialog"
    append-to-body
    destroy-on-close
  >
    <ElInput
      v-model="keyword"
      size="large"
      clearable
      autofocus
      placeholder="搜索城市名称或拼音"
      aria-label="搜索目的地城市"
    >
      <template #prefix><Search aria-hidden="true" /></template>
    </ElInput>

    <ElTabs v-model="scope" class="destination-tabs" stretch>
      <ElTabPane label="国内" name="domestic" />
      <ElTabPane label="国外" name="international" />
    </ElTabs>

    <div v-if="selected.length" class="selected-cities" aria-label="已选目的地">
      <ElTag v-for="item in selected" :key="item" closable size="large" @close="remove(item)">{{
        item
      }}</ElTag>
    </div>

    <div class="city-results" aria-live="polite">
      <ElSkeleton v-if="loading" :rows="6" animated />
      <ElEmpty v-else-if="!results.length" :description="prompt" :image-size="72" />
      <ul v-else>
        <li v-for="city in results" :key="city[0]">
          <button
            type="button"
            :class="{ 'is-selected': isSelected(city) }"
            :aria-pressed="isSelected(city)"
            @click="toggle(city)"
          >
            <span>
              <strong>{{ cityLabel(city) }}</strong>
              <small>{{ cityDetail(city) }}</small>
            </span>
            <X v-if="isSelected(city)" aria-hidden="true" />
            <span v-else class="select-mark" aria-hidden="true">+</span>
          </button>
        </li>
      </ul>
    </div>

    <p class="data-attribution">城市数据由 GeoNames 提供</p>

    <template #footer>
      <ElButton @click="opened = false">取消</ElButton>
      <ElButton type="primary" @click="confirm">确认选择（{{ selected.length }}）</ElButton>
    </template>
  </ElDialog>
</template>

<style scoped>
.destination-tabs {
  margin-top: 16px;
}
.selected-cities {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 14px;
}
.selected-cities :deep(.el-tag) {
  max-width: 100%;
}
.city-results {
  height: min(380px, 46vh);
  overflow-y: auto;
}
.city-results ul {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.city-results button {
  display: flex;
  width: 100%;
  min-height: 58px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--tf-line-soft);
  border-radius: var(--tf-radius-control);
  background: var(--tf-surface-sunken);
  color: var(--tf-text-1);
  cursor: pointer;
  text-align: left;
}
.city-results button:hover,
.city-results button.is-selected {
  border-color: var(--tf-accent);
  background: var(--tf-accent-soft);
}
.city-results button:focus-visible {
  outline: 2px solid var(--tf-accent);
  outline-offset: 2px;
}
.city-results button > span:first-child {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}
.city-results strong,
.city-results small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.city-results small {
  color: var(--tf-text-3);
  font-size: 12px;
}
.city-results svg,
.select-mark {
  width: 18px;
  height: 18px;
  flex: 0 0 18px;
  color: var(--tf-accent);
}
.select-mark {
  display: grid;
  place-items: center;
  font-size: 20px;
  line-height: 1;
}
.data-attribution {
  margin: 10px 0 0;
  color: var(--tf-text-3);
  font-size: 11px;
  text-align: right;
}
@media (prefers-reduced-motion: no-preference) {
  .city-results button {
    transition:
      border-color var(--tf-duration-fast) var(--tf-ease),
      background var(--tf-duration-fast) var(--tf-ease),
      transform var(--tf-duration-fast) var(--tf-ease);
  }
  .city-results button:active {
    transform: scale(0.98);
  }
}
@media (max-width: 600px) {
  .city-results ul {
    grid-template-columns: 1fr;
  }
}
</style>
