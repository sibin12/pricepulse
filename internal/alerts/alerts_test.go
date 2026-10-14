package alerts

import (
	"strings"
	"testing"
	"time"
)

func TestFormatMoney(t *testing.T) {
	cases := []struct {
		amount   float64
		currency string
		want     string
	}{
		{11499, "INR", "₹11,499.00"},
		{100, "USD", "$100.00"},
		{1234567.5, "eur", "€1,234,567.50"},
		{9.99, "CHF", "CHF 9.99"},
		{42, "", "42.00"},
	}
	for _, c := range cases {
		if got := formatMoney(c.amount, c.currency); got != c.want {
			t.Errorf("formatMoney(%v, %q) = %q; want %q", c.amount, c.currency, got, c.want)
		}
	}
}

func TestBuildMIMEBlocksHeaderInjection(t *testing.T) {
	msg := Message{
		To:      []string{"me@example.com"},
		Subject: "Deal!\r\nBcc: attacker@evil.example",
		Text:    "body ₹",
	}
	raw := string(buildMIME("PricePulse <alerts@example.com>", msg, time.Now()))

	head, _, _ := strings.Cut(raw, "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("header injection succeeded:\n%s", head)
		}
	}
	if !strings.Contains(raw, "Content-Transfer-Encoding: quoted-printable") || !strings.Contains(raw, "=E2=82=B9") {
		t.Fatal("UTF-8 body (₹) should be quoted-printable encoded")
	}
}

func TestAddressOnly(t *testing.T) {
	if got := addressOnly("PricePulse <alerts@example.com>"); got != "alerts@example.com" {
		t.Fatal(got)
	}
	if got := addressOnly(" me@example.com "); got != "me@example.com" {
		t.Fatal(got)
	}
}
