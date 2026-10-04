import { inject, onScopeDispose, provide, shallowRef, type InjectionKey } from 'vue'
import type { Asset } from '@/shared/api/assets'
import { randomId } from '@/shared/randomId'
import { useAssetUpload } from '@/shared/travel/useAssetUpload'

export interface AssetTransfer {
  key: string
  name: string
  upload: ReturnType<typeof useAssetUpload>
}
const key: InjectionKey<ReturnType<typeof createTransfers>> = Symbol('trip-transfers')
function createTransfers() {
  const transfers = shallowRef<AssetTransfer[]>([])
  function add(file: File, tripId: string, registered?: (id: string) => void, existing?: Asset) {
    const upload = useAssetUpload(registered)
    const transfer = { key: randomId(), name: file.name, upload }
    transfers.value = [...transfers.value, transfer]
    void (existing ? upload.resume(existing, file) : upload.start({ file, scope: 'trip', tripId }))
    return transfer
  }
  function find(id: string) {
    return [...transfers.value].reverse().find((item) => item.upload.state.assetId === id)
  }
  function remove(transfer: AssetTransfer) {
    transfer.upload.cancel()
    transfers.value = transfers.value.filter((item) => item !== transfer)
  }
  onScopeDispose(() => transfers.value.forEach((item) => item.upload.cancel()))
  return { transfers, add, find, remove }
}
export function provideTripTransfers() {
  const value = createTransfers()
  provide(key, value)
  return value
}
export function useTripTransfers() {
  return inject(key, null) ?? createTransfers()
}
