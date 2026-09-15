/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** API 前缀。网页开发与同源部署用相对路径 /api/v1；Capacitor 打包必须是绝对地址。 */
  readonly VITE_API_BASE_URL?: string
  /** JS API 公开 Key；安全密钥 AMAP_JSCODE 仅由开发/生产代理读取。 */
  readonly VITE_AMAP_JS_KEY?: string
  readonly VITE_AMAP_SERVICE_HOST?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
