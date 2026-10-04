import { onScopeDispose, ref, shallowRef } from 'vue'
import { ElMessageBox } from 'element-plus'
import {
  actionError,
  createWriteIntent,
  writeWarnings,
  type WriteOutcome,
} from '@/shared/api/writes'

export function useContentCollection<T extends { id: string; version: string }>(
  list: (cursor?: string) => Promise<{ items: T[]; next_cursor: string | null }>,
  remove: (item: T, operationId: string) => Promise<WriteOutcome<T>>,
) {
  const items = shallowRef<T[]>([])
  const cursor = ref<string | null>(null)
  const loading = ref(false)
  const error = ref('')
  const notice = ref('')
  const deleting = ref('')
  let generation = 0
  const intents = new Map<string, ReturnType<typeof createWriteIntent>>()
  async function reload(more = false) {
    if (more && (loading.value || !cursor.value)) return
    const run = ++generation
    loading.value = true
    error.value = ''
    try {
      const page = await list(more ? cursor.value! : undefined)
      if (run !== generation) return
      items.value = more
        ? [...items.value, ...page.items.filter((v) => !items.value.some((i) => i.id === v.id))]
        : page.items
      cursor.value = page.next_cursor
    } catch (cause) {
      if (run === generation) error.value = actionError(cause, '无法加载内容，请重试。')
    } finally {
      if (run === generation) loading.value = false
    }
  }
  async function saved(outcome: WriteOutcome<T>) {
    notice.value = ['已保存。', ...writeWarnings(outcome.result)].join(' ')
    await reload()
  }
  async function destroy(item: T, message: string): Promise<boolean> {
    if (deleting.value) return false
    try {
      await ElMessageBox.confirm(message, '确认删除', {
        confirmButtonText: '删除',
        cancelButtonText: '取消',
        type: 'warning',
      })
    } catch {
      return false
    }
    deleting.value = item.id
    error.value = ''
    const intent = intents.get(item.id) ?? createWriteIntent()
    intents.set(item.id, intent)
    try {
      await remove(item, intent.key({ id: item.id, version: item.version }))
      intents.delete(item.id)
      notice.value = '已删除。'
      await reload()
      return true
    } catch (cause) {
      const failure = actionError(cause, '删除结果尚未确认，可重试或刷新核对。')
      await reload()
      error.value = failure
      return false
    } finally {
      deleting.value = ''
    }
  }
  onScopeDispose(() => {
    generation++
  })
  return { items, cursor, loading, error, notice, deleting, reload, saved, destroy }
}
