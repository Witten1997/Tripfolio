package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SMTPConfig 是 SMTP 投递配置。阿里云邮件推送：smtpdm.aliyun.com:465，用户名为发信地址。
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	FromName string
	// ImplicitTLS 为 true 时使用 465 端口的隐式 TLS；否则先明文再 STARTTLS。
	ImplicitTLS bool
	Timeout     time.Duration
}

// SMTPMailer 通过标准库 net/smtp 发送。
type SMTPMailer struct {
	cfg SMTPConfig
}

// NewSMTPMailer 创建 SMTP 投递器。
func NewSMTPMailer(cfg SMTPConfig) *SMTPMailer {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &SMTPMailer{cfg: cfg}
}

// Send 实现 Mailer。
func (m *SMTPMailer) Send(ctx context.Context, msg Message) error {
	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	dialer := &net.Dialer{Timeout: m.cfg.Timeout}
	tlsCfg := &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}

	var client *smtp.Client
	if m.cfg.ImplicitTLS {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("连接 SMTP: %w", err)
		}
		client, err = smtp.NewClient(conn, m.cfg.Host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("SMTP 握手: %w", err)
		}
	} else {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("连接 SMTP: %w", err)
		}
		client, err = smtp.NewClient(conn, m.cfg.Host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("SMTP 握手: %w", err)
		}
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				client.Close()
				return fmt.Errorf("STARTTLS: %w", err)
			}
		}
	}
	defer client.Close()

	if m.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return fmt.Errorf("SMTP 认证: %w", err)
		}
	}
	if err := client.Mail(m.cfg.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write([]byte(m.build(msg))); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("发送正文: %w", err)
	}
	return client.Quit()
}

func (m *SMTPMailer) build(msg Message) string {
	from := m.cfg.From
	if m.cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", mime.QEncoding.Encode("utf-8", m.cfg.FromName), m.cfg.From)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(msg.Text, "\n", "\r\n"))
	b.WriteString("\r\n")
	return b.String()
}
