package alerts

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strings"
	"sync"
	"time"

	"pricepulse/internal/domain"
	"pricepulse/internal/repository"
)

// ErrDisabled is returned by SendTest when email is not configured.
var ErrDisabled = errors.New("email alerts are not configured")

// Notifier sends "target hit" emails. It works like an outbox: the database
// says which alerts are due (price at/below target, not yet sent), and the
// notifier keeps delivering them until each one succeeds. A failed send is
// simply retried later; no alert is lost because of a mail server hiccup.
type Notifier struct {
	repo   *repository.ProductRepository
	mailer Mailer // nil when email is not configured
	appURL string
	logger *log.Logger

	poll  time.Duration
	retry time.Duration
	wake  chan struct{} // Nudge signals here to send immediately

	mu      sync.Mutex
	backoff map[int64]time.Time // product ID -> don't retry before this time
}

// Config for New. Recipients aren't configured here: each alert goes to the
// email address of the product's owner.
type Config struct {
	AppURL string        // base URL of the web UI, used for links in emails
	Poll   time.Duration // how often to look for due alerts (default 30s)
	Retry  time.Duration // wait after a failed send before retrying (default 5m)
}

// New creates a Notifier. Pass a nil mailer to disable email; the rest of the
// app keeps working and the UI explains how to enable it.
func New(repo *repository.ProductRepository, mailer Mailer, logger *log.Logger, cfg Config) *Notifier {
	if cfg.Poll <= 0 {
		cfg.Poll = 30 * time.Second
	}
	if cfg.Retry <= 0 {
		cfg.Retry = 5 * time.Minute
	}
	return &Notifier{
		repo:    repo,
		mailer:  mailer,
		appURL:  strings.TrimRight(cfg.AppURL, "/"),
		logger:  logger,
		poll:    cfg.Poll,
		retry:   cfg.Retry,
		wake:    make(chan struct{}, 1),
		backoff: map[int64]time.Time{},
	}
}

// Enabled reports whether emails can be sent.
func (n *Notifier) Enabled() bool { return n.mailer != nil }

// Nudge asks the notifier to look for due alerts right away, e.g. just after
// a new price was recorded. It never blocks: if a wake-up is already pending,
// the extra one is dropped (one pass handles everything that's due).
func (n *Notifier) Nudge() {
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// Run sends due alerts until ctx is cancelled.
func (n *Notifier) Run(ctx context.Context) {
	if !n.Enabled() {
		n.logger.Println("email alerts disabled (set SMTP_HOST to enable)")
		return
	}
	n.logger.Println("email alerts enabled (sent to each product owner)")
	ticker := time.NewTicker(n.poll)
	defer ticker.Stop()
	for {
		n.sendDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-n.wake:
		}
	}
}

// sendDue emails every product that is due, one at a time (alert volume is
// tiny, and sequential sends keep us well within mail provider rate limits).
func (n *Notifier) sendDue(ctx context.Context) {
	due, err := n.repo.AlertsDue(ctx, 50)
	if err != nil {
		if ctx.Err() == nil {
			n.logger.Printf("alerts: list due: %v", err)
		}
		return
	}
	for _, d := range due {
		p := d.Product
		if ctx.Err() != nil {
			return
		}
		if n.inBackoff(p.ID) {
			continue
		}

		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := n.mailer.Send(sendCtx, n.targetHitMessage(&p, d.Email))
		cancel()
		if err != nil {
			n.setBackoff(p.ID, time.Now().Add(n.retry))
			n.logger.Printf("alerts: email for product %d failed (retrying in %s): %v", p.ID, n.retry, err)
			continue
		}

		n.setBackoff(p.ID, time.Time{})
		if err := n.repo.MarkAlertSent(context.WithoutCancel(ctx), p.ID, time.Now().UTC()); err != nil {
			// The email went out but we couldn't record it; worst case the user
			// gets a duplicate later, which beats silently losing an alert.
			n.logger.Printf("alerts: email sent for product %d but not recorded: %v", p.ID, err)
			continue
		}
		n.logger.Printf("alerts: emailed %s: %q is %s (target %s)",
			d.Email, p.Title, formatMoney(p.CurrentPrice, p.Currency), formatMoney(p.TargetPrice, p.Currency))
	}
}

// SendTest sends a sample email to `to` so a user can verify delivery.
func (n *Notifier) SendTest(ctx context.Context, to string) error {
	if !n.Enabled() {
		return ErrDisabled
	}
	return n.mailer.Send(ctx, Message{
		To:      []string{to},
		Subject: "PricePulse test email",
		Text: "Email alerts are working.\n\n" +
			"You'll get an email like this whenever a tracked product drops to your target price.\n\n" +
			n.appURL + "\n",
		HTML: emailHTML("Email alerts are working",
			"<p>You'll get an email like this whenever a tracked product drops to your target price.</p>",
			n.appURL, "Open PricePulse"),
	})
}

func (n *Notifier) targetHitMessage(p *domain.Product, to string) Message {
	price := formatMoney(p.CurrentPrice, p.Currency)
	target := formatMoney(p.TargetPrice, p.Currency)
	link := fmt.Sprintf("%s/products/%d", n.appURL, p.ID)

	text := fmt.Sprintf("%s is now %s — at or below your target of %s.\n\nBuy it: %s\nPrice history: %s\n",
		p.Title, price, target, p.URL, link)

	body := fmt.Sprintf(
		`<p style="font-size:16px;margin:0 0 4px"><strong>%s</strong></p>`+
			`<p style="font-size:28px;font-weight:700;color:#0f9d58;margin:8px 0">%s</p>`+
			`<p style="color:#646b82;margin:0 0 16px">Your target: %s</p>`+
			`<p><a href="%s">View in store →</a></p>`,
		html.EscapeString(p.Title), price, target, html.EscapeString(p.URL))

	return Message{
		To:      []string{to},
		Subject: fmt.Sprintf("Price drop: %s is now %s", truncate(p.Title, 70), price),
		Text:    text,
		HTML:    emailHTML("Your target price was hit", body, link, "See price history"),
	}
}

// emailHTML wraps content in a minimal, inline-styled layout (email clients
// ignore <style> blocks and external CSS, so styles must be inline).
func emailHTML(heading, body, buttonURL, buttonText string) string {
	return fmt.Sprintf(`<!doctype html><html><body style="margin:0;padding:24px;background:#f6f7fb;font-family:Arial,sans-serif;color:#161a2b">
<div style="max-width:520px;margin:0 auto;background:#fff;border:1px solid #e3e6ef;border-radius:12px;padding:24px">
<p style="margin:0 0 16px;font-weight:700;color:#4f46e5">PricePulse</p>
<h1 style="font-size:20px;margin:0 0 16px">%s</h1>
%s
<p style="margin:24px 0 0"><a href="%s" style="background:#4f46e5;color:#fff;text-decoration:none;padding:10px 16px;border-radius:8px;display:inline-block">%s</a></p>
</div></body></html>`, html.EscapeString(heading), body, html.EscapeString(buttonURL), html.EscapeString(buttonText))
}

func (n *Notifier) inBackoff(id int64) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return time.Now().Before(n.backoff[id])
}

func (n *Notifier) setBackoff(id int64, until time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if until.IsZero() {
		delete(n.backoff, id)
	} else {
		n.backoff[id] = until
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
