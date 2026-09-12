// Package problem 输出 application/problem+json 错误响应（接口设计 1.4）。
// 它不依赖其他传输层包，供中间件与处理器共用。
package problem

import (
	"encoding/json"
	"net/http"
)

// ContentType 是错误响应的媒体类型。
const ContentType = "application/problem+json; charset=utf-8"

// Body 是错误响应正文。字段顺序与接口设计示例一致。
type Body struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Code      string       `json:"code"`
	Detail    string       `json:"detail,omitempty"`
	RequestID string       `json:"request_id"`
	Errors    []FieldError `json:"errors,omitempty"`
	Conflict  *Conflict    `json:"conflict,omitempty"`
}

// FieldError 是 422 VALIDATION_FAILED 的字段级错误。
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Conflict 是 412 VERSION_CONFLICT 的上下文。
type Conflict struct {
	EntityType        string   `json:"entity_type"`
	EntityID          string   `json:"entity_id"`
	ExpectedVersion   string   `json:"expected_version"`
	CurrentVersion    string   `json:"current_version"`
	ConflictingFields []string `json:"conflicting_fields"`
	Current           any      `json:"current"`
	DeletionContext   any      `json:"deletion_context,omitempty"`
}

// titles 是错误代码到标题的映射；未登记的代码用 code 本身作标题。
var titles = map[string]string{
	"MALFORMED_REQUEST":      "请求格式错误",
	"INVALID_CURSOR":         "分页游标无效",
	"AUTH_REQUIRED":          "需要登录",
	"INVALID_CREDENTIALS":    "邮箱或密码不正确",
	"SESSION_EXPIRED":        "登录已失效",
	"ACCOUNT_DELETING":       "账号正在注销",
	"REAUTH_REQUIRED":        "需要重新验证密码",
	"CSRF_FAILED":            "跨站请求校验失败",
	"RESOURCE_NOT_FOUND":     "资源不存在",
	"METHOD_NOT_ALLOWED":     "方法不允许",
	"IDEMPOTENCY_CONFLICT":   "重复操作的内容不一致",
	"ID_ALREADY_USED":        "ID 已被使用",
	"ACCOUNT_ALREADY_EXISTS": "邮箱已注册",
	"CURRENCY_LOCKED":        "币种已锁定",
	"CURRENCY_AMOUNTS_EXIST": "需先清空预算与预计费用",
	"CATEGORY_IN_USE":        "分类仍被使用",
	"ORDER_CHANGED":          "行程集合已变化",
	"ASSET_NOT_READY":        "文件尚未就绪",
	"UPLOAD_NOT_ALLOWED":     "当前状态不允许上传",
	"RESTORE_UNAVAILABLE":    "旅行已进入永久清理",
	"TRIP_DELETED":           "旅行在回收站中",
	"RESOURCE_GONE":          "资源已删除",
	"CURSOR_EXPIRED":         "同步游标已过期",
	"UPLOAD_EXPIRED":         "上传已超时",
	"DOWNLOAD_EXPIRED":       "下载已过期",
	"VERSION_CONFLICT":       "资源已被修改",
	"VERSION_REQUIRED":       "缺少资源版本",
	"VALIDATION_FAILED":      "字段校验失败",
	"REFUND_AMOUNT_EXCEEDED": "退款超过原支出",
	"CURRENCY_MISMATCH":      "币种与旅行不一致",
	"INVALID_REFERENCE":      "关联对象无效",
	"REQUEST_TOO_LARGE":      "请求正文过大",
	"UNSUPPORTED_MEDIA_TYPE": "不支持的媒体类型",
	"RATE_LIMITED":           "请求过于频繁",
	"INTERNAL_ERROR":         "服务内部错误",
	"DEPENDENCY_UNAVAILABLE": "依赖服务不可用",
}

// Title 返回代码对应的标题。
func Title(code string) string {
	if t, ok := titles[code]; ok {
		return t
	}
	return code
}

// Write 写出错误响应。requestID 由调用方从请求上下文取得。
func Write(w http.ResponseWriter, status int, code, detail, requestID string) {
	WriteBody(w, Body{
		Type:      "about:blank",
		Title:     Title(code),
		Status:    status,
		Code:      code,
		Detail:    detail,
		RequestID: requestID,
	})
}

// WriteBody 写出完整正文，供需要附带 errors 或 conflict 的调用方使用。
func WriteBody(w http.ResponseWriter, body Body) {
	if body.Type == "" {
		body.Type = "about:blank"
	}
	if body.Title == "" {
		body.Title = Title(body.Code)
	}
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(body.Status)
	_ = json.NewEncoder(w).Encode(body)
}
