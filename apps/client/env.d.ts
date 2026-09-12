/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** API 前缀。网页开发与同源部署用相对路径 /api/v1；Capacitor 打包必须是绝对地址。 */
  readonly VITE_API_BASE_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
