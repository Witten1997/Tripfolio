// Package apperr 定义业务错误：携带 HTTP 状态、稳定错误代码与可选的字段级明细或版本冲突上下文。
// 传输层把它映射为 application/problem+json（接口设计 1.4）。
package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// FieldError 是 422 VALIDATION_FAILED 的字段级错误。
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Conflict 是 412 VERSION_CONFLICT 的上下文。ConflictingFields 为 nil 表示因日志过期无法判断。
type Conflict struct {
	EntityType        string    `json:"entity_type"`
	EntityID          uuid.UUID `json:"entity_id"`
	ExpectedVersion   int64     `json:"expected_version"`
	CurrentVersion    int64     `json:"current_version"`
	ConflictingFields []string  `json:"conflicting_fields"`
	Current           any       `json:"current"`
	DeletionContext   any       `json:"deletion_context,omitempty"`
}

// Error 是可映射为 problem+json 的业务错误。
type Error struct {
	Status   int
	Code     string
	Detail   string
	Fields   []FieldError
	Conflict *Conflict
	// Headers 附加到响应头，例如 Retry-After。
	Headers map[string]string
	cause   error
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Detail)
	}
	return e.Code
}

// Unwrap 返回底层原因，便于 errors.Is 判断。
func (e *Error) Unwrap() error { return e.cause }

// WithCause 记录底层错误（只进日志，不进响应）。
func (e *Error) WithCause(cause error) *Error {
	e.cause = cause
	return e
}

// New 创建任意状态与代码的错误。
func New(status int, code, detail string) *Error {
	return &Error{Status: status, Code: code, Detail: detail}
}

// As 判断 err 是否为业务错误。
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// NotFound 对应不存在、非本人或路径错属的资源。
func NotFound() *Error { return New(http.StatusNotFound, "RESOURCE_NOT_FOUND", "") }

// Gone 对应 410 的各类代码（RESOURCE_GONE、TRIP_DELETED 等）。
func Gone(code, detail string) *Error { return New(http.StatusGone, code, detail) }

// Validation 对应 422 VALIDATION_FAILED。
func Validation(fields ...FieldError) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "VALIDATION_FAILED", Fields: fields}
}

// Unprocessable 对应 422 的其他代码（REFUND_AMOUNT_EXCEEDED、INVALID_REFERENCE 等）。
func Unprocessable(code, detail string) *Error {
	return New(http.StatusUnprocessableEntity, code, detail)
}

// Field 构造字段错误。
func Field(field, code, message string) FieldError {
	return FieldError{Field: field, Code: code, Message: message}
}

// Conflicted 对应 409 的各类代码。
func Conflicted(code, detail string) *Error { return New(http.StatusConflict, code, detail) }

// Unauthorized 对应 401。
func Unauthorized(code, detail string) *Error { return New(http.StatusUnauthorized, code, detail) }

// Forbidden 对应 403。
func Forbidden(code, detail string) *Error { return New(http.StatusForbidden, code, detail) }

// BadRequest 对应 400。
func BadRequest(code, detail string) *Error { return New(http.StatusBadRequest, code, detail) }

// VersionRequired 对应 428。
func VersionRequired() *Error {
	return New(http.StatusPreconditionRequired, "VERSION_REQUIRED", "请求需要携带 If-Match 版本")
}

// VersionConflict 对应 412。
func VersionConflict(c *Conflict) *Error {
	return &Error{Status: http.StatusPreconditionFailed, Code: "VERSION_CONFLICT", Detail: "资源已被修改，请核对后重试", Conflict: c}
}

// RateLimited 对应 429 并附带 Retry-After。
func RateLimited(retryAfter time.Duration) *Error {
	secs := int(retryAfter.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return &Error{
		Status:  http.StatusTooManyRequests,
		Code:    "RATE_LIMITED",
		Detail:  "请求过于频繁，请稍后再试",
		Headers: map[string]string{"Retry-After": strconv.Itoa(secs)},
	}
}

// Internal 包装未预期的错误；响应不含原因。
func Internal(cause error) *Error {
	return New(http.StatusInternalServerError, "INTERNAL_ERROR", "").WithCause(cause)
}

// Dependency 表示依赖服务不可用。
func Dependency(cause error) *Error {
	return New(http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "").WithCause(cause)
}
