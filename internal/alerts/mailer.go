// Package alerts emails the user when a tracked product's price drops to (or
// below) its target.
package alerts

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
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Message is one email. Text is required; HTML is optional (sent as an
// alternative part that mail clients prefer when they can render it).
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
}

// Mailer sends email. The SMTP implementation is below; tests use a fake.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// SMTPConfig holds mail server settings, normally read from env vars.
type SMTPConfig struct {
	Host     string // e.g. smtp.gmail.com
	Port     int    // 587 (STARTTLS, the usual choice) or 465 (implicit TLS)
	Username string // empty = no authentication (e.g. a local relay)
	Password string
	From     string // e.g. "PricePulse <alerts@example.com>"
}

// SMTPMailer sends mail with the standard library's net/smtp.
type SMTPMailer struct {
	cfg SMTPConfig
}

func NewSMTPMailer(cfg SMTPConfig) *SMTPMailer {
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	return &SMTPMailer{cfg: cfg}
}

// Send delivers msg. net/smtp has no context support, so the context's
// deadline (or 30s) is applied to the underlying connection instead.
func (m *SMTPMailer) Send(ctx context.Context, msg Message) error {
	if len(msg.To) == 0 {
		return errors.New("no recipients")
	}
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}

	dialer := &net.Dialer{Deadline: deadline}
	var conn net.Conn
	var err error
	if m.cfg.Port == 465 {
		// Port 465 speaks TLS from the first byte ("implicit TLS").
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: m.cfg.Host})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	// Every read/write after this point fails once the deadline passes, so a
	// stuck mail server can't hang the alert loop.
	conn.SetDeadline(deadline)

	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer c.Close()

	// On port 587, upgrade to TLS before sending credentials.
	if ok, _ := c.Extension("STARTTLS"); ok && m.cfg.Port != 465 {
		if err := c.StartTLS(&tls.Config{ServerName: m.cfg.Host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if m.cfg.Username != "" {
		// PlainAuth refuses to send the password over an unencrypted
		// connection (except to localhost), protecting it from sniffing.
		if err := c.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	from := addressOnly(m.cfg.From)
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, to := range msg.To {
		if err := c.Rcpt(addressOnly(to)); err != nil {
			return fmt.Errorf("smtp RCPT TO %s: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(buildMIME(m.cfg.From, msg, time.Now())); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finish message: %w", err)
	}
	return c.Quit()
}

// buildMIME renders msg as a multipart/alternative email (plain text + HTML),
// UTF-8 encoded so symbols like ₹ and € survive any mail server.
func buildMIME(from string, msg Message, now time.Time) []byte {
	boundary := randomHex(12)
	var b bytes.Buffer

	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	header("From", from)
	header("To", strings.Join(msg.To, ", "))
	// Q-encoding makes non-ASCII subjects legal in a header.
	header("Subject", mime.QEncoding.Encode("utf-8", sanitizeHeader(msg.Subject)))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", fmt.Sprintf("<%s@pricepulse>", randomHex(16)))
	header("MIME-Version", "1.0")
	header("Content-Type", fmt.Sprintf(`multipart/alternative; boundary="%s"`, boundary))
	b.WriteString("\r\n")

	part := func(contentType, body string) {
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		fmt.Fprintf(&b, "Content-Type: %s; charset=utf-8\r\n", contentType)
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		qp := quotedprintable.NewWriter(&b)
		qp.Write([]byte(body))
		qp.Close()
		b.WriteString("\r\n")
	}
	part("text/plain", msg.Text)
	if msg.HTML != "" {
		part("text/html", msg.HTML)
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

// sanitizeHeader removes CR/LF. Product titles come from scraped web pages, so
// without this a page could inject extra headers (e.g. "Bcc:") into our email.
func sanitizeHeader(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\r' || r == '\n' }), " ")
}

// addressOnly turns "Name <a@b.com>" into "a@b.com" for the SMTP envelope.
func addressOnly(s string) string {
	if i, j := strings.LastIndex(s, "<"), strings.LastIndex(s, ">"); i >= 0 && j > i {
		return strings.TrimSpace(s[i+1 : j])
	}
	return strings.TrimSpace(s)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}
