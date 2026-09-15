import {
  normalizePackingStatus,
  packingCategoryLabels,
  packingCategoryOrder,
  type PackingCategory,
  type PackingItem,
  type PackingStatus,
} from '@/shared/api/packing'

export interface PackingFilters {
  category: '' | PackingCategory
  status: '' | PackingStatus
}

/** 保持预设大类顺序，仅返回当前筛选下确实有物品的分组。 */
export function packingGroups(items: readonly PackingItem[], filters: PackingFilters) {
  return packingCategoryOrder
    .filter((category) => !filters.category || filters.category === category)
    .map((category) => ({
      category,
      label: packingCategoryLabels[category],
      items: items.filter(
        (item) =>
          item.category === category &&
          (!filters.status ||
            normalizePackingStatus(item.status) === normalizePackingStatus(filters.status)),
      ),
    }))
    .filter((group) => group.items.length > 0)
}

export function packingProgress(items: readonly PackingItem[]) {
  const total = items.length
  const ready = items.filter((item) => normalizePackingStatus(item.status) === 'ready').length
  return { total, ready, percent: total ? Math.round((ready / total) * 100) : 0 }
}
