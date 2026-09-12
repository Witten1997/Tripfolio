# @tripfolio/contracts

API 契约的唯一来源。目录：

- `openapi/v1/openapi.yaml`：入口；`paths/` 按资源拆分路径，`schemas/` 拆分模型。
- `dist/openapi.v1.yaml`：`redocly bundle` 打包后的单文件，供 Go（oapi-codegen）与 TypeScript（openapi-typescript）生成代码。提交入库。
- `generated/openapi.v1.d.ts`：前端使用的类型，`import type { paths } from '@tripfolio/contracts/openapi/v1'`。提交入库。
- `fixtures/`：跨语言测试样例（金额规范化等），Go 与前端测试都读取同一文件。

## 命令

```bash
pnpm --filter @tripfolio/contracts build   # lint + bundle + gen:ts
pnpm --filter @tripfolio/contracts check   # 构建后检查生成物与源文件一致（CI 用）
```

修改契约后还要在后端运行：

```bash
cd apps/server && go generate ./internal/transport/httpapi/
```

## 规则

- 与 `docs/api/2026-09-11-P0接口设计.md` 保持一致；文档描述与契约冲突时以契约为准并回改文档。
- 错误代码只在接口设计 1.4 的清单中登记；契约里以 `Problem.code` 字符串表达，不做枚举以免每次新增代码都破坏客户端。
- 不手工修改 `dist/` 与 `generated/`。
