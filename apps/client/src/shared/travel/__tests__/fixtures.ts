import type { ExpenseCategory } from '@/shared/api/categories'
import type { TripListItem } from '@/shared/api/trips'
import type { WriteResult } from '@/shared/api/writes'
import type { Metadata } from '@/shared/stores/metadata'

export const tripId = '10000000-0000-4000-8000-000000000001'
export const categoryId = '20000000-0000-4000-8000-000000000001'

export function trip(overrides: Partial<TripListItem> = {}): TripListItem {
  return {
    id: tripId,
    name: '秋日杭州',
    start_date: '2026-10-01',
    end_date: '2026-10-07',
    destination: '杭州',
    notes: '',
    timezone: 'Asia/Shanghai',
    currency_code: 'CNY',
    budget_amount: '5000.00',
    version: '3',
    phase: 'planned',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-02T00:00:00Z',
    archived_at: null,
    currency_locked_at: null,
    deleted_at: null,
    purge_after_at: null,
    purge_requested_at: null,
    ...overrides,
  }
}

export function category(overrides: Partial<ExpenseCategory> = {}): ExpenseCategory {
  return {
    id: categoryId,
    name: '交通',
    icon: 'transport',
    sort_order: 5,
    is_preset: true,
    version: '2',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    deleted_at: null,
    ...overrides,
  }
}

export const metadata: Metadata = {
  protocol_version: 1,
  default_currency_code: 'CNY',
  currencies: [
    { code: 'CNY', minor_units: 2 },
    { code: 'JPY', minor_units: 0 },
    { code: 'KWD', minor_units: 3 },
  ],
  ledger_kinds: ['expense', 'refund'],
  expense_category_icons: ['transport', 'lodging', 'food', 'other'],
  packing_categories: [],
  packing_statuses: [],
  packing_library_version: '1',
  itinerary_kinds: [],
  itinerary_statuses: [],
  reservation_kinds: [],
  upload_limits: {
    image_max_bytes: 1,
    pdf_max_bytes: 1,
    image_media_types: [],
    pdf_media_types: [],
  },
  map: { provider: 'amap', coordinate_system: 'GCJ-02' },
}

export function receipt(
  resource: object | null,
  warnings: string[] = [],
  replayed = false,
): WriteResult {
  return {
    operation_id: '30000000-0000-4000-8000-000000000001',
    primary: null,
    affected: [],
    commit_cursor: null,
    warnings,
    replayed,
    data: resource ? { ...resource } : null,
  }
}

export function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

export function problem(code: string, status = 409, extra: object = {}) {
  return new Response(
    JSON.stringify({
      type: 'about:blank',
      title: code,
      status,
      code,
      request_id: 'req-travel-test',
      ...extra,
    }),
    { status, headers: { 'Content-Type': 'application/problem+json' } },
  )
}

export function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => {
    resolve = yes
    reject = no
  })
  return { promise, resolve, reject }
}
