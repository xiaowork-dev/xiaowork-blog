// Package mail sends transactional e-mail over SMTP. The SMTP server, credentials and TLS mode come
// from configuration, so a self-hosted domain mailbox can replace the development catcher later.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/config"
)

// Message is a plain-text e-mail to a single recipient.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Mailer delivers messages. Implementations must honour ctx cancellation/deadlines.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// SMTPMailer delivers through the configured SMTP server.
type SMTPMailer struct {
	cfg config.SMTP
}

func NewSMTP(cfg config.SMTP) *SMTPMailer { return &SMTPMailer{cfg: cfg} }

// Send opens one connection per message (low volume transactional mail) with a hard deadline.
func (m *SMTPMailer) Send(ctx context.Context, msg Message) error {
	data, err := build(m.cfg.From, msg, time.Now())
	if err != nil {
		return err
	}
	deadline := time.Now().Add(m.cfg.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	tlsConfig := &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{}
	var conn net.Conn
	if m.cfg.TLS == config.SMTPTLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	_ = conn.SetDeadline(deadline)
	// Abort the conversation if ctx is cancelled mid-way.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer c.Close()

	if m.cfg.TLS == config.SMTPTLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("smtp: server does not offer STARTTLS")
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if m.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(m.cfg.From.Address); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := c.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp end of data: %w", err)
	}
	return c.Quit()
}

// build renders RFC 5322 headers and a quoted-printable UTF-8 body. CR/LF in header values is rejected
// to rule out header injection.
func build(from *mail.Address, msg Message, now time.Time) ([]byte, error) {
	if strings.ContainsAny(msg.To+msg.Subject, "\r\n") {
		return nil, errors.New("mail: header values must not contain line breaks")
	}
	to, err := mail.ParseAddress(msg.To)
	if err != nil {
		return nil, fmt.Errorf("mail: invalid recipient: %w", err)
	}
	idBytes := make([]byte, 16)
	_, _ = rand.Read(idBytes)
	domain := from.Address[strings.LastIndex(from.Address, "@")+1:]

	var b bytes.Buffer
	header := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	header("From", from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", now.UTC().Format(time.RFC1123Z))
	header("Message-ID", "<"+hex.EncodeToString(idBytes)+"@"+domain+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=UTF-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	header("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	body := strings.ReplaceAll(strings.ReplaceAll(msg.Text, "\r\n", "\n"), "\n", "\r\n")
	if _, err := qp.Write([]byte(body)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Recorder is an in-memory Mailer for tests. Setting Err makes every Send fail.
type Recorder struct {
	mu       sync.Mutex
	messages []Message
	Err      error
}

func (r *Recorder) Send(_ context.Context, msg Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.messages = append(r.messages, msg)
	return nil
}

// Messages returns a copy of what was sent so far.
func (r *Recorder) Messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.messages...)
}

// SetErr changes the failure mode safely while requests may be running.
func (r *Recorder) SetErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Err = err
}
