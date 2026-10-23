package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/config"
)

func TestBuildRejectsHeaderInjection(t *testing.T) {
	from := &mail.Address{Name: "XiaoWork Blog", Address: "no-reply@example.com"}
	if _, err := build(from, Message{To: "a@example.com\r\nBcc: x@evil.test", Subject: "s", Text: "t"}, time.Now()); err == nil {
		t.Fatal("CRLF in recipient accepted")
	}
	if _, err := build(from, Message{To: "a@example.com", Subject: "s\nBcc: x@evil.test", Text: "t"}, time.Now()); err == nil {
		t.Fatal("CRLF in subject accepted")
	}
	data, err := build(from, Message{To: "a@example.com", Subject: "验证你的邮箱", Text: "你好\n链接"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "Subject: =?utf-8?q?") || !strings.Contains(s, "Content-Transfer-Encoding: quoted-printable") {
		t.Fatalf("unexpected message:\n%s", s)
	}
}

// Network failure: a refused connection fails fast with an error instead of hanging.
func TestSMTPConnectionRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close() // nothing listens on this port any more
	m := NewSMTP(config.SMTP{Host: "127.0.0.1", Port: port, TLS: config.SMTPTLSNone, Timeout: 2 * time.Second,
		From: &mail.Address{Address: "no-reply@example.com"}})
	start := time.Now()
	if err := m.Send(context.Background(), Message{To: "a@example.com", Subject: "s", Text: "t"}); err == nil {
		t.Fatal("expected a dial error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("send did not respect the timeout")
	}
}

// A server that accepts the TCP connection but never speaks SMTP must hit the deadline.
func TestSMTPTimeout(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close() // hold the connection silently
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port
	m := NewSMTP(config.SMTP{Host: "127.0.0.1", Port: port, TLS: config.SMTPTLSNone, Timeout: time.Second,
		From: &mail.Address{Address: "no-reply@example.com"}})
	start := time.Now()
	if err := m.Send(context.Background(), Message{To: "a@example.com", Subject: "s", Text: "t"}); err == nil {
		t.Fatal("expected a timeout")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("send took %s", d)
	}
}

// Real delivery to a Mailpit test server (TEST_SMTP_ADDR=host:port, TEST_MAILPIT_API=http://host:8025).
func TestSMTPDeliveryToMailpit(t *testing.T) {
	addr, api := os.Getenv("TEST_SMTP_ADDR"), os.Getenv("TEST_MAILPIT_API")
	if addr == "" || api == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "1" {
			t.Fatal("TEST_SMTP_ADDR and TEST_MAILPIT_API are required")
		}
		t.Skip("TEST_SMTP_ADDR/TEST_MAILPIT_API not set")
	}
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	to := fmt.Sprintf("smtp-test-%d@example.test", time.Now().UnixNano())
	m := NewSMTP(config.SMTP{Host: host, Port: port, TLS: config.SMTPTLSNone, Timeout: 5 * time.Second,
		From: &mail.Address{Name: "XiaoWork Blog", Address: "no-reply@xiaowork.test"}})
	if err := m.Send(context.Background(), Message{To: to, Subject: "测试邮件", Text: "正文 body"}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(api + "/api/v1/search?query=" + "to:" + to)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var found struct {
		MessagesCount int `json:"messages_count"`
		Messages      []struct {
			Subject string `json:"Subject"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&found); err != nil {
		t.Fatal(err)
	}
	if found.MessagesCount != 1 || found.Messages[0].Subject != "测试邮件" {
		t.Fatalf("mailpit search = %+v", found)
	}
}
