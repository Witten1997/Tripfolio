import type { NetworkStatus } from '../types'

export const webNetwork: NetworkStatus = {
  async isOnline() {
    return window.navigator.onLine
  },
  onChange(listener) {
    const online = () => listener(true)
    const offline = () => listener(false)
    window.addEventListener('online', online)
    window.addEventListener('offline', offline)
    return () => {
      window.removeEventListener('online', online)
      window.removeEventListener('offline', offline)
    }
  },
}
