// Package expense holds the shared domain types and money parsing.
package expense

import (
	"errors"
	"fmt"
	"strings"
)

// ParseMoney converts a printed amount ("$1,234.50", "EUR 12,40", "1.234,50")
// into minor units (cents). It never uses floats. Negative or zero values are
// parsed as-is; callers decide whether they are acceptable.
func ParseMoney(s string) (int64, error) {
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r == '.', r == ',':
			b.WriteRune(r)
		case r == '-':
			neg = true
		}
	}
	num := b.String()
	if num == "" {
		return 0, fmt.Errorf("no digits in %q", s)
	}
	dec := decimalSeparator(num)
	var whole, frac string
	if dec == 0 {
		whole = strings.NewReplacer(",", "", ".", "").Replace(num)
	} else {
		i := strings.LastIndexByte(num, dec)
		whole = strings.NewReplacer(",", "", ".", "").Replace(num[:i])
		frac = num[i+1:]
	}
	if len(frac) > 2 {
		return 0, fmt.Errorf("too many decimal places in %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	if whole == "" {
		whole = "0"
	}
	if len(whole) > 12 {
		return 0, errors.New("amount too large")
	}
	var v int64
	for _, r := range whole + frac {
		v = v*10 + int64(r-'0')
	}
	if neg {
		v = -v
	}
	return v, nil
}

// decimalSeparator returns '.' or ',' if the last separator is a decimal
// point, or 0 if the number has only thousands separators.
func decimalSeparator(num string) byte {
	last := strings.LastIndexAny(num, ".,")
	if last < 0 {
		return 0
	}
	sep := num[last]
	digitsAfter := len(num) - last - 1
	other := byte('.')
	if sep == '.' {
		other = ','
	}
	if strings.IndexByte(num, other) >= 0 {
		return sep // mixed separators: the last one is the decimal point
	}
	if strings.Count(num, string(sep)) > 1 {
		return 0 // repeated: thousands separator
	}
	if digitsAfter == 3 && last > 0 {
		return 0 // "1,234" or "1.234": thousands
	}
	return sep
}

// FormatMoney renders minor units as "1234.50".
func FormatMoney(minor int64) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100)
}
