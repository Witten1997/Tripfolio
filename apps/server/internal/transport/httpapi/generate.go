// Package httpapi 是 HTTP 传输层：请求解码、身份传递、参数转换、响应与错误映射。
// 业务规则在 internal/modules，跨模块协调在 internal/workflows；本包不直接调用 sqlc 代码。
//
// 契约来源是 packages/contracts，生成命令如下（先运行 `pnpm --filter @tripfolio/contracts build`）。
package httpapi

//go:generate go tool oapi-codegen -config ../../../oapi-codegen.yaml ../../../../../packages/contracts/dist/openapi.v1.yaml
