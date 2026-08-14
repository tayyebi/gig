package services

import (
	"context"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"

	"github.com/tayyebi/gig/config"
)

// MailerFromConfig returns an SMTP-backed mailer when SMTP_HOST is
// configured, or a LogMailer otherwise. The web server and the worker both
// call this so they always agree on how mail is delivered.
func MailerFromConfig(cfg *config.Config, log *slog.Logger) Mailer {
	if cfg.SMTPHost == "" {
		return &LogMailer{Log: log}
	}
	return NewSMTPMailer(SMTPConfig{
		Host: cfg.SMTPHost,
		Port: cfg.SMTPPort,
		User: cfg.SMTPUser,
		Pass: cfg.SMTPPass,
		From: cfg.EmailFrom,
	}, log)
}

// Email is a plain-text message ready for delivery.
type Email struct {
	To      string
	Subject string
	Body    string
}

// Mailer sends transactional emails. Handlers depend on this interface so
// delivery can be swapped between a real SMTP client and a development logger.
type Mailer interface {
	Send(ctx context.Context, email Email) error
}

// LogMailer writes outgoing mail to the application log. It is the default
// for development so no SMTP server is required. The full body is logged
// deliberately: without it, one-time links would be unrecoverable in dev.
type LogMailer struct {
	Log *slog.Logger
}

// Send logs the email at Info level.
func (m *LogMailer) Send(_ context.Context, email Email) error {
	m.Log.Info("email queued (log mailer)",
		"to", email.To,
		"subject", email.Subject,
		"body", email.Body,
	)
	return nil
}

// SMTPConfig holds connection settings for SMTPMailer.
type SMTPConfig struct {
	Host string
	Port string
	User string
	Pass string
	From string
}

// SMTPMailer delivers email over SMTP (plain or STARTTLS).
type SMTPMailer struct {
	cfg SMTPConfig
	log *slog.Logger
}

// NewSMTPMailer returns an SMTP-backed mailer.
func NewSMTPMailer(cfg SMTPConfig, log *slog.Logger) *SMTPMailer {
	return &SMTPMailer{cfg: cfg, log: log}
}

// Send delivers a message over SMTP.
func (m *SMTPMailer) Send(ctx context.Context, email Email) error {
	addr := fmt.Sprintf("%s:%s", m.cfg.Host, m.cfg.Port)
	var auth smtp.Auth
	if m.cfg.User != "" {
		auth = smtp.PlainAuth("", m.cfg.User, m.cfg.Pass, m.cfg.Host)
	}
	msg := buildSMTPMessage(m.cfg.From, email)
	if err := smtp.SendMail(addr, auth, m.cfg.From, []string{email.To}, msg); err != nil {
		m.log.Error("smtp send failed", "to", email.To, "subject", email.Subject, "err", err)
		return fmt.Errorf("smtp send: %w", err)
	}
	m.log.Info("email sent", "to", email.To, "subject", email.Subject)
	return nil
}

func buildSMTPMessage(from string, email Email) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + email.To + "\r\n")
	b.WriteString("Subject: " + email.Subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(email.Body)
	return []byte(b.String())
}
