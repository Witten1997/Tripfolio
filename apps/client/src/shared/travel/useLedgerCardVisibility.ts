import { onMounted, ref } from 'vue'

import { platform } from '@/platform'

export const ledgerCardOptions = [
  { id: 'statistics', label: '支出统计' },
  { id: 'category-share', label: '分类占比' },
  { id: 'daily-net', label: '每日净支出' },
  { id: 'settlement', label: '成员结算' },
  { id: 'category-amounts', label: '分类金额' },
  { id: 'entries', label: '账目明细' },
] as const

type LedgerCardId = (typeof ledgerCardOptions)[number]['id']

export function useLedgerCardVisibility(tripId: string) {
  const preferenceKey = `tripfolio.trip.${tripId}.ledger-cards`
  const visibleCards = ref<LedgerCardId[]>(ledgerCardOptions.map((card) => card.id))
  const ready = ref(false)
  const opened = ref(false)
  const draft = ref<LedgerCardId[]>([])
  const saving = ref(false)
  const error = ref<string | null>(null)

  onMounted(async () => {
    try {
      const saved = await platform.preferences.get(preferenceKey)
      const preferences: unknown = saved ? JSON.parse(saved) : null
      if (preferences && typeof preferences === 'object' && !Array.isArray(preferences)) {
        const values = preferences as Record<string, unknown>
        visibleCards.value = ledgerCardOptions
          .filter((card) => values[card.id] !== false)
          .map((card) => card.id)
      }
    } catch {
      // 本地偏好不可读时保留默认展示，不阻塞账单页面。
    } finally {
      ready.value = true
    }
  })

  function isVisible(id: LedgerCardId) {
    return ready.value && visibleCards.value.includes(id)
  }

  function open() {
    if (!ready.value) return
    draft.value = [...visibleCards.value]
    error.value = null
    opened.value = true
  }

  function close() {
    if (!saving.value) opened.value = false
  }

  async function save() {
    if (saving.value) return
    const selected = ledgerCardOptions.filter((card) => draft.value.includes(card.id))
    saving.value = true
    error.value = null
    try {
      await platform.preferences.set(
        preferenceKey,
        JSON.stringify(
          Object.fromEntries(
            ledgerCardOptions.map((card) => [card.id, draft.value.includes(card.id)]),
          ),
        ),
      )
      visibleCards.value = selected.map((card) => card.id)
      opened.value = false
    } catch {
      error.value = '无法保存卡片配置，请重试。'
    } finally {
      saving.value = false
    }
  }

  return { visibleCards, ready, opened, draft, saving, error, isVisible, open, close, save }
}
