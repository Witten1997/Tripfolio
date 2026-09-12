import type { PluginListenerHandle } from '@capacitor/core'
import { Network } from '@capacitor/network'

import type { NetworkStatus } from '../types'

export const capacitorNetwork: NetworkStatus = {
  async isOnline() {
    const status = await Network.getStatus()
    return status.connected
  },
  onChange(listener) {
    let handle: PluginListenerHandle | undefined
    let cancelled = false
    void Network.addListener('networkStatusChange', (status) => listener(status.connected)).then(
      (h) => {
        if (cancelled) void h.remove()
        else handle = h
      },
    )
    return () => {
      cancelled = true
      void handle?.remove()
    }
  },
}
