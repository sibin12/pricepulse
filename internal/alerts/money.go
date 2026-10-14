package alerts

import (
	"strconv"
	"strings"
)

// symbols covers common currencies; anything else is shown with its ISO code.
var symbols = map[string]string{
	"USD": "$", "EUR": "€", "GBP": "£", "INR": "₹", "JPY": "¥",
	"AUD": "A$", "CAD": "C$", "SGD": "S$", "AED": "AED ",
}

// formatMoney renders 11499 + "INR" as "₹11,499.00". Go's standard library has
// no locale-aware currency formatting, so this is a deliberately simple version
// (always comma-grouped thousands, two decimals).
func formatMoney(amount float64, currency string) string {
	s := strconv.FormatFloat(amount, 'f', 2, 64)
	whole, frac, _ := strings.Cut(s, ".")

	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	num := b.String() + "." + frac

	code := strings.ToUpper(currency)
	if sym, ok := symbols[code]; ok {
		return sym + num
	}
	if code != "" {
		return code + " " + num
	}
	return num
}
