import type { CapacitorConfig } from '@capacitor/cli'

// Capacitor 8：安卓页面来源为 https://localhost，与 API 不同源，凭证走 Bearer + 安全存储，
// API 与对象存储的 CORS 允许来源需包含该来源（技术选型 4.3）。
const config: CapacitorConfig = {
  appId: 'com.tripfolio.app',
  appName: 'Tripfolio',
  webDir: 'dist',
  server: {
    androidScheme: 'https',
  },
  android: {
    allowMixedContent: false,
  },
  plugins: {
    // 本地数据库不加密：内容是用户自己的旅行数据，文件在应用私有目录；凭证另存安全存储
    CapacitorSQLite: {
      androidIsEncryption: false,
    },
  },
}

export default config
