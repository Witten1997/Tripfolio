import { createPinia } from 'pinia'
import { createApp } from 'vue'

import App from './App.vue'
import { platform } from './platform'
import { createAppRouter } from './router'
import { pickShell } from './shell/pickShell'
import './style.css'

// 启动时一次性决定壳：Capacitor 原生环境或窄视口用移动壳，其余用桌面壳。
const shell = pickShell({ isNative: platform.isNative, viewportWidth: window.innerWidth })

const app = createApp(App, { shell })
app.use(createPinia())
app.use(createAppRouter(shell))
app.mount('#app')
