import { computed, inject, provide, ref, shallowRef, type InjectionKey, type Ref } from 'vue'

import { ApiError } from '@/shared/api/auth'
import { getTrip, type Trip } from '@/shared/api/trips'
import { actionError } from '@/shared/api/writes'
import { useMetadataStore } from '@/shared/stores/metadata'
import { todayIn } from '@/shared/travel/tripDays'

export interface TripContext {
  tripId: string
  trip: Ref<Trip | null>
  loading: Ref<boolean>
  error: Ref<string | null>
  /** 加载失败时的稳定错误代码（RESOURCE_NOT_FOUND、TRIP_DELETED 等），供页面决定入口。 */
  errorCode: Ref<string | null>
  /** 旅行时区下的今天，供行程“今日”高亮与待办逾期展示。 */
  today: Ref<string>
  minorUnits: Ref<number>
  reload: () => Promise<void>
  /** 页签内写入返回了新版旅行（例如编辑旅行对话框）时同步到上下文。 */
  replace: (trip: Trip) => void
  /** 成员管理保存后递增，账单页据此刷新成员选项与结算。 */
  membersVersion: Ref<number>
  bumpMembers: () => void
}

const key: InjectionKey<TripContext> = Symbol('trip-context')

/** 详情页加载旅行并向页签提供；旅行不存在、非本人或在回收站时记录错误由页面展示。 */
export function provideTripContext(tripId: string): TripContext {
  const metadata = useMetadataStore()
  const trip = shallowRef<Trip | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)
  const errorCode = ref<string | null>(null)
  const now = ref(Date.now())
  const membersVersion = ref(0)
  let generation = 0

  async function reload() {
    const request = ++generation
    loading.value = true
    error.value = errorCode.value = null
    try {
      const loaded = await getTrip(tripId)
      if (request === generation) {
        trip.value = loaded
        now.value = Date.now()
      }
    } catch (cause) {
      if (request === generation) {
        error.value = actionError(cause, '无法加载旅行，请检查网络后重试')
        errorCode.value = cause instanceof ApiError ? (cause.code ?? null) : null
      }
    } finally {
      if (request === generation) loading.value = false
    }
  }

  const context: TripContext = {
    tripId,
    trip,
    loading,
    error,
    errorCode,
    today: computed(() => todayIn(trip.value?.timezone ?? 'UTC', new Date(now.value))),
    minorUnits: computed(() => metadata.minorUnits(trip.value?.currency_code ?? 'CNY')),
    reload,
    replace: (next) => {
      trip.value = next
      now.value = Date.now()
    },
    membersVersion,
    bumpMembers: () => {
      membersVersion.value++
    },
  }
  provide(key, context)
  return context
}

export function useTripContext(): TripContext {
  const context = inject(key)
  if (!context) throw new Error('useTripContext 必须在旅行详情页内使用')
  return context
}
