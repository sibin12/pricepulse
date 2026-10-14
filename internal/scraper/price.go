package scraper

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

var errNotAPrice = errors.New("not a price")

// parsePrice turns human/HTML price text into a number, handling the common
// international formats:
//
//	"$1,299.99"   -> 1299.99   (comma thousands, dot decimal)
//	"1.299,99 €"  -> 1299.99   (dot thousands, comma decimal)
//	"₹1,29,999"   -> 129999    (Indian digit grouping)
//	"19,99"       -> 19.99     (comma decimal)
//	"1 299,00"    -> 1299      (space thousands)
//
// Only the first number in the string is used, so "£10.00 – £20.00" -> 10.
func parsePrice(s string) (float64, error) {
	num := firstNumber(s)
	if num == "" {
		return 0, errNotAPrice
	}

	lastComma := strings.LastIndex(num, ",")
	lastDot := strings.LastIndex(num, ".")

	switch {
	case lastComma >= 0 && lastDot >= 0:
		// Both present: whichever comes last is the decimal separator.
		if lastComma > lastDot {
			num = strings.ReplaceAll(num, ".", "")
			num = strings.Replace(num, ",", ".", 1)
		} else {
			num = strings.ReplaceAll(num, ",", "")
		}
	case lastComma >= 0:
		// Only commas: a single comma followed by 1–2 digits is a decimal
		// ("19,99"); anything else is thousands grouping ("1,299", "1,29,999").
		if strings.Count(num, ",") == 1 && len(num)-lastComma-1 <= 2 {
			num = strings.Replace(num, ",", ".", 1)
		} else {
			num = strings.ReplaceAll(num, ",", "")
		}
	case strings.Count(num, ".") > 1:
		// "1.299.000" — several dots can only be thousands separators.
		num = strings.ReplaceAll(num, ".", "")
	}

	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, errNotAPrice
	}
	return math.Round(f*100) / 100, nil
}

// firstNumber returns the first run of digits (with internal , . or spaces
// used as grouping) found in s, e.g. "Now only $1 299,00!" -> "1299,00".
func firstNumber(s string) string {
	var b strings.Builder
	started := false
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			started = true
		case started && (r == ',' || r == '.'):
			b.WriteRune(r)
		case started && (r == ' ' || r == ' ' || r == ' '):
			// A space only counts as grouping when a digit follows ("1 299").
			if next := s[i+len(string(r)):]; next == "" || next[0] < '0' || next[0] > '9' {
				return strings.TrimRight(b.String(), ",.")
			}
		case started:
			return strings.TrimRight(b.String(), ",.")
		}
	}
	return strings.TrimRight(b.String(), ",.")
}
