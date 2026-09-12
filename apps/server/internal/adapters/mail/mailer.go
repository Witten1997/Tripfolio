// Package mail 投递验证码等事务邮件。实现有两种：日志（开发与测试）与 SMTP（阿里云邮件推送等）。
package mail

import (
	"context"
	"log/slog"
)

// Message 是一封纯文本邮件。
type Message struct {
	To      string
	Subject string
	Text    string
}

// Mailer 发送邮件。实现不得把正文写入日志，除非是显式的开发实现。
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// LogMailer 把邮件写入日志，仅用于开发环境：验证码会出现在日志中。
type LogMailer struct {
	Logger *slog.Logger
}

// Send 实现 Mailer。
func (m LogMailer) Send(ctx context.Context, msg Message) error {
	m.Logger.WarnContext(ctx, "开发模式邮件（未真正发送）", "to", msg.To, "subject", msg.Subject, "text", msg.Text)
	return nil
}
