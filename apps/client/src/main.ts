import { createPinia } from 'pinia'
import { createApp } from 'vue'

import { isSharePath } from './share/path'
import App from './App.vue'
import { platform } from './platform'
import { createAppRouter } from './router'
import { useThemeStore } from './shared/stores/theme'
import { pickShell } from './shell/pickShell'
import './styles/base.css'

// 启动时一次性决定壳：Capacitor 原生环境或窄视口用移动壳，其余用桌面壳。
const shell = pickShell({ isNative: platform.isNative, viewportWidth: window.innerWidth })

const share = isSharePath(window.location.pathname)

async function bootstrap() {
  const app = createApp(App, { shell, share })
  const pinia = createPinia()
  app.use(pinia)
  // 主题在挂载前恢复，首屏直接以正确主题渲染，避免闪烁
  await useThemeStore(pinia).restore({ defaultOnly: share })
  app.use(createAppRouter(shell))
  app.mount('#app')
}

void bootstrap()
