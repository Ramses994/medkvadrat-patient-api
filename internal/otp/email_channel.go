package otp

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
)

type EmailChannel struct {
	cfg config.SMTPConfig
}

func NewEmailChannel(cfg config.SMTPConfig) EmailChannel {
	return EmailChannel{cfg: cfg}
}

func (c EmailChannel) Name() string { return "email" }

func (c EmailChannel) Send(ctx context.Context, patient Patient, code string) error {
	_ = ctx // stdlib net/smtp has no context; timeouts are enforced via dialer.

	host := c.cfg.Host
	port := c.cfg.Port
	if host == "" || port == 0 {
		return fmt.Errorf("smtp not configured")
	}

	from := c.cfg.FromEmail
	if from == "" {
		from = c.cfg.User
	}
	if from == "" {
		return fmt.Errorf("smtp from not configured")
	}

	to := strings.TrimSpace(patient.Email)
	if to == "" {
		return fmt.Errorf("missing recipient email")
	}

	subject := fmt.Sprintf("Код для входа в личный кабинет: %s", code)
	textBody := fmt.Sprintf(
		"Здравствуйте!\n\nВаш код для входа: %s\n\nКод действует 5 минут.\nЕсли вы не запрашивали код, просто проигнорируйте это письмо.\n",
		code,
	)
	htmlBody := fmt.Sprintf(
		`<div style="font-family:Arial,sans-serif;line-height:1.5">
<p>Здравствуйте!</p>
<p>Ваш код для входа:</p>
<p style="font-size:28px;font-weight:700;letter-spacing:2px">%s</p>
<p>Код действует 5 минут.</p>
<p style="color:#666">Если вы не запрашивали код, просто проигнорируйте это письмо.</p>
</div>`,
		code,
	)

	msg, err := buildMultipart(fromNameAddr(c.cfg.FromName, from), to, subject, textBody, htmlBody)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	// custom dialer for timeouts
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if c.cfg.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	} else if c.cfg.TLSMode == "tls" {
		// For implicit TLS we'd need to dial with tls; not implemented in MVP.
		return fmt.Errorf("smtp tls mode not supported: %s", c.cfg.TLSMode)
	}

	if c.cfg.User != "" {
		auth := smtp.PlainAuth("", c.cfg.User, c.cfg.Password, host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	return client.Quit()
}

func fromNameAddr(name, email string) string {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name == "" {
		return email
	}
	// Minimal RFC 5322: no fancy encoding yet.
	return fmt.Sprintf("%s <%s>", name, email)
}

func buildMultipart(from, to, subject, textBody, htmlBody string) ([]byte, error) {
	boundary := "medkvadrat_boundary_otp"
	var buf bytes.Buffer
	write := func(s string) { _, _ = buf.WriteString(s) }

	write(fmt.Sprintf("From: %s\r\n", from))
	write(fmt.Sprintf("To: %s\r\n", to))
	write(fmt.Sprintf("Subject: %s\r\n", subject))
	write("MIME-Version: 1.0\r\n")
	write(fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q\r\n", boundary))
	write("\r\n")

	write(fmt.Sprintf("--%s\r\n", boundary))
	write("Content-Type: text/plain; charset=UTF-8\r\n")
	write("Content-Transfer-Encoding: 8bit\r\n\r\n")
	write(textBody + "\r\n")

	write(fmt.Sprintf("--%s\r\n", boundary))
	write("Content-Type: text/html; charset=UTF-8\r\n")
	write("Content-Transfer-Encoding: 8bit\r\n\r\n")
	write(htmlBody + "\r\n")

	write(fmt.Sprintf("--%s--\r\n", boundary))
	return buf.Bytes(), nil
}
