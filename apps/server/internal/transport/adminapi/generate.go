// Package adminapi 提供独立的后台 HTTP 接口；契约来自 packages/contracts/openapi/admin。
package adminapi

//go:generate go tool oapi-codegen -config ../../../admin-oapi-codegen.yaml ../../../../../packages/contracts/dist/openapi.admin.yaml
