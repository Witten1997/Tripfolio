# @tripfolio/client

一个 Vue 3 工程，两个壳：

- `src/desktop/`：桌面浏览器壳（Element Plus），宽屏多日编排与整理。
- `src/mobile/`：手机浏览器与 Capacitor 应用共用的移动壳（Vant），四页签旅行详情、快捷记账、拍照、清单勾选。
- `src/shared/`：两壳共用的接口客户端、状态、金额与日期规则、同步逻辑。
- `src/platform/`：平台接口与实现（`web/`、`capacitor/`，后续 `ohos/`）。业务代码只依赖 `types.ts`。
- `src/router/`：同一份路由表，按壳映射到不同页面组件。

启动时 `pickShell` 决定壳：Capacitor 原生环境或视口宽度不超过 767 时用移动壳。

## 命令

```bash
pnpm install                      # 在仓库根执行
pnpm --filter @tripfolio/contracts build   # 契约类型（apps/client 依赖 @tripfolio/contracts）
pnpm --filter @tripfolio/client dev        # http://localhost:5173，/api 代理到 localhost:8080
pnpm --filter @tripfolio/client typecheck
pnpm --filter @tripfolio/client test
pnpm --filter @tripfolio/client build      # 产物 dist/，供打包进后端单二进制与 Capacitor 打包
```

## Capacitor（安卓）

当前行程选点和地图页面沿用现有 Element Plus 业务组件，移动路由暂复用并做窄屏适配；独立 Vant 页面及安卓真机验收随移动端推进，共享的地点／路线 API 与算路逻辑可复用。

```bash
pnpm --filter @tripfolio/client build
pnpm --filter @tripfolio/client exec cap add android    # 首次生成 android/ 工程（已生成则跳过）
pnpm --filter @tripfolio/client cap:sync                # 复制 dist 与插件到 android/
pnpm --filter @tripfolio/client cap:open                # 用 Android Studio 打开并构建
```

- 构建 APK 需要 JDK 21 与 Android SDK（Capacitor 8 要求）；本仓库不提交 `android/` 的构建产物。
- 应用来源是 `https://localhost`，与 API 不同源：`VITE_API_BASE_URL` 必须是绝对地址，凭证走 Bearer + 安全存储，API 的 CORS 允许来源需包含该来源。
- 相册选取的 HEIC 需转为 JPEG 后上传。

## 地图与地点选择

在 `.env.local` 配置 `VITE_AMAP_JS_KEY`（JS API Key）与 `AMAP_JSCODE`（安全密钥），重启 Vite。不要将安全密钥命名为 `VITE_AMAP_JSCODE`，也不要配置到 `window._AMapSecurityConfig.securityJsCode`：代码只设置 `serviceHost`，安全密钥由 Vite 开发代理（本地）或应用进程内的 `/_AMapService` 代理（部署）追加。

`VITE_AMAP_SERVICE_HOST` 默认同源 `/_AMapService`。跨域或 Capacitor 构建须设为可访问的 HTTPS 代理完整路径（例如 `https://api.example.com/_AMapService`）；生产由单二进制进程内的代理提供该路径，`vite preview` 不是生产代理。POI 搜索和三种算路走 `/api/v1/geo`，需已登录，后端须配置独立的 Web 服务 Key。

旅行详情“行程”中新建／编辑时选点；“地图”页签按日期与行程顺序展示点位和道路路线，账单统计头部也有入口。搜索支持防抖与取消；地图有可键盘操作的点位列表、缩放按钮和坐标输入替代路径。无路线时显示示意虚线，并从道路里程汇总中排除。地址文字手动修改会清除旧坐标，需重新选点后才参与路线。

需要独立联调后端时可设置 Vite 进程环境变量 `TRIPFOLIO_DEV_API_TARGET`（如 `http://127.0.0.1:18080`）；未设置仍代理到 `http://localhost:8080`。该变量不会进入浏览器包。

## 本地数据库（离线）

`src/platform/localdb/` 是与平台无关的本地数据库层（接口、迁移、仓储、自检场景），安卓由 `src/platform/capacitor/localDatabase.ts` 用 @capacitor-community/sqlite 实现，单元测试用 Node 内置 sqlite 跑同一套代码。每个账号一个数据库文件 `tripfolio-<账号ID>`。真机验证入口：移动壳“旅行”页 → 开发工具 → 本地数据库自检（路由 `/dev/local-db`）。设计与验证步骤见 `docs/architecture/2026-09-12-本地数据库数据链路.md`。

## 约定

- 金额一律用 `canonicalizeAmount` 规范化后提交，规则与服务端相同，样例共用 `@tripfolio/contracts/fixtures/money.json`。
- 访问令牌只放内存（`useSessionStore`）；不把凭证写入 localStorage。
- 页面组件放各自壳目录，跨壳复用的逻辑放 `shared/`，不在页面里直接调用平台 API。
