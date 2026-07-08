package executor

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"path/filepath"

	"github.com/naibabiji/server-panel/database"
)

type SMTPConfig struct {
	Host       string
	Port       string
	Encryption string
	User       string
	Pass       string
	AdminEmail string
}

type MailAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

func GetSMTPConfig() *SMTPConfig {
	db := database.GetDB()
	if db == nil {
		return nil
	}
	cfg := &SMTPConfig{}
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'smtp_host'").Scan(&cfg.Host)
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'smtp_port'").Scan(&cfg.Port)
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'smtp_encryption'").Scan(&cfg.Encryption)
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'smtp_user'").Scan(&cfg.User)
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'smtp_pass'").Scan(&cfg.Pass)
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'admin_email'").Scan(&cfg.AdminEmail)
	return cfg
}

func SendMail(to, subject, body string) error {
	return sendMailMessage(to, subject, buildMessage, body, nil)
}

func SendMailWithAttachments(to, subject, body string, attachments []MailAttachment) error {
	return sendMailMessage(to, subject, buildMessageWithAttachments, body, attachments)
}

func sendMailMessage(to, subject string, builder func(string, string, string, string, []MailAttachment) (string, error), body string, attachments []MailAttachment) error {
	cfg := GetSMTPConfig()
	if cfg == nil || cfg.Host == "" || cfg.User == "" || cfg.Pass == "" {
		return fmt.Errorf("SMTP 未配置")
	}
	if to == "" {
		to = cfg.AdminEmail
	}
	if to == "" {
		return fmt.Errorf("管理员邮箱未设置")
	}

	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	msg, err := builder(cfg.User, to, subject, body, attachments)
	if err != nil {
		return err
	}

	switch cfg.Encryption {
	case "ssl":
		tlsCfg := &tls.Config{ServerName: cfg.Host}
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("TLS 连接失败: %w", err)
		}
		defer conn.Close()
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return fmt.Errorf("SMTP 客户端创建失败: %w", err)
		}
		defer client.Quit()
		if err := authAndSend(client, cfg, to, msg); err != nil {
			return err
		}
	case "none":
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return fmt.Errorf("连接失败: %w", err)
		}
		defer conn.Close()
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return fmt.Errorf("SMTP 客户端创建失败: %w", err)
		}
		defer client.Quit()
		if err := client.Mail(cfg.User); err != nil {
			return err
		}
		if err := client.Rcpt(to); err != nil {
			return err
		}
		wc, err := client.Data()
		if err != nil {
			return err
		}
		_, err = wc.Write([]byte(msg))
		wc.Close()
		if err != nil {
			return err
		}
	default: // starttls
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return fmt.Errorf("连接失败: %w", err)
		}
		defer conn.Close()
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return fmt.Errorf("SMTP 客户端创建失败: %w", err)
		}
		defer client.Quit()
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
			return fmt.Errorf("STARTTLS 失败: %w", err)
		}
		if err := authAndSend(client, cfg, to, msg); err != nil {
			return err
		}
	}
	return nil
}

func authAndSend(client *smtp.Client, cfg *SMTPConfig, to, msg string) error {
	auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("认证失败: %w", err)
	}
	if err := client.Mail(cfg.User); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	wc, err := client.Data()
	if err != nil {
		return err
	}
	_, err = wc.Write([]byte(msg))
	wc.Close()
	return err
}

func buildMessage(from, to, subject, body string, _ []MailAttachment) (string, error) {
	return fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		from, to, mime.QEncoding.Encode("utf-8", subject), body), nil
}

func buildMessageWithAttachments(from, to, subject, body string, attachments []MailAttachment) (string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", writer.Boundary())

	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=UTF-8")
	textHeader.Set("Content-Transfer-Encoding", "8bit")
	textPart, err := writer.CreatePart(textHeader)
	if err != nil {
		return "", err
	}
	if _, err := textPart.Write([]byte(body)); err != nil {
		return "", err
	}

	for _, attachment := range attachments {
		filename := filepath.Base(attachment.Filename)
		if filename == "." || filename == string(filepath.Separator) {
			filename = "attachment"
		}
		contentType := attachment.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Type", contentType)
		header.Set("Content-Transfer-Encoding", "base64")
		header.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, mime.QEncoding.Encode("utf-8", filename)))
		part, err := writer.CreatePart(header)
		if err != nil {
			return "", err
		}
		encoder := base64.NewEncoder(base64.StdEncoding, newBase64LineWriter(part))
		if _, err := encoder.Write(attachment.Data); err != nil {
			_ = encoder.Close()
			return "", err
		}
		if err := encoder.Close(); err != nil {
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

type base64LineWriter struct {
	w      interface{ Write([]byte) (int, error) }
	column int
}

func newBase64LineWriter(w interface{ Write([]byte) (int, error) }) *base64LineWriter {
	return &base64LineWriter{w: w}
}

func (w *base64LineWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		remaining := 76 - w.column
		if remaining <= 0 {
			if _, err := w.w.Write([]byte("\r\n")); err != nil {
				return written, err
			}
			w.column = 0
			remaining = 76
		}
		if remaining > len(p) {
			remaining = len(p)
		}
		if _, err := w.w.Write(p[:remaining]); err != nil {
			return written, err
		}
		written += remaining
		w.column += remaining
		p = p[remaining:]
	}
	return written, nil
}

func TestSMTP(to string) error {
	return SendMail(to, "Server Panel — 测试邮件", "如果您收到这封邮件，说明 SMTP 配置正确。\n\n来自 Server Panel。")
}
