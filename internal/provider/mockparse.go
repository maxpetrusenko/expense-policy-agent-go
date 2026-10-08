package provider

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	reLabeled  = regexp.MustCompile(`(?im)^\W*(?:grand total|total due|amount due|balance due|total)\b[^\d\n]*([\d][\d.,]*)`)
	reAnyTotal = regexp.MustCompile(`(?im)^.*total.*?([\d][\d.,]*)\s*$`)
	reNumber   = regexp.MustCompile(`\d[\d.,]*\d|\d`)
	reMoney    = regexp.MustCompile(`\d+[.,]\d{2}\b`)
	reTaxLine  = regexp.MustCompile(`(?im)^\W*(?:sales tax|tax|vat|gst|hst)\b.*$`)
	reVendor   = regexp.MustCompile(`(?im)^\s*(?:from|vendor|merchant)\s*:\s*(.+)$`)
	reGuests   = regexp.MustCompile(`(?im)^\s*(?:attendees|guests|party|diners)\s*:\s*(.+)$`)
	reSplit    = regexp.MustCompile(`\s*(?:,|;|&|\band\b)\s*`)
	reISO      = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	reSlash    = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})/(\d{4})\b`)
	reDots     = regexp.MustCompile(`\b(\d{1,2})\.(\d{1,2})\.(\d{4})\b`)
	reMDY      = regexp.MustCompile(`(?i)\b(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+(\d{1,2}),?\s+(\d{4})\b`)
	reDMY      = regexp.MustCompile(`(?i)\b(\d{1,2})\s+(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?,?\s+(\d{4})\b`)
	reSkipHead = regexp.MustCompile(`(?i)^(receipt|invoice|tax invoice|customer copy|card slip|thank you|welcome)\b`)
	reAlcV2    = regexp.MustCompile(`(?i)\b(wine|beer|lager|ipa|ale|cocktail|whiske?y|vodka|gin|prosecco|champagne|margarita|bourbon|sake|cabernet|chardonnay|pinot|mojito)\b`)
	reAlcV1    = regexp.MustCompile(`(?i)\b(wine|beer)\b`)
	reSlip     = regexp.MustCompile(`(?i)card slip|cardholder copy|merchant copy|approval code|auth code`)
	reInvoice  = regexp.MustCompile(`(?i)\binvoice\s*(#|no\b|number)`)
	reCard     = regexp.MustCompile(`(?i)\bvisa\b|mastercard|\bamex\b|american express|\bdebit\b|credit card|card ending|\*{2,}\s?\d{4}`)
	reCash     = regexp.MustCompile(`(?i)\bcash\b`)
	reOther    = regexp.MustCompile(`(?i)paypal|venmo|cheque|\bcheck no|\bwire\b|\bach\b|bank transfer`)
	reEUR      = regexp.MustCompile(`€|\bEUR\b`)
	reGBP      = regexp.MustCompile(`£|\bGBP\b`)
	reCAD      = regexp.MustCompile(`CA\$|\bCAD\b`)
	reAUD      = regexp.MustCompile(`\bAUD\b`)
	reJPY      = regexp.MustCompile(`¥|\bJPY\b`)
	reCHF      = regexp.MustCompile(`\bCHF\b`)
	reUSD      = regexp.MustCompile(`\bUSD\b`)
	reLineSkip = regexp.MustCompile(`(?i)total|tax|vat|gst|tip|change|balance|due|visa|mastercard|amex`)
)

var months = map[string]string{"jan": "01", "feb": "02", "mar": "03", "apr": "04", "may": "05", "jun": "06",
	"jul": "07", "aug": "08", "sep": "09", "oct": "10", "nov": "11", "dec": "12"}

func findMerchant(t string) (string, float64) {
	if m := reVendor.FindStringSubmatch(t); m != nil {
		return strings.TrimSpace(m[1]), 0.9
	}
	for _, l := range strings.Split(t, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || reSkipHead.MatchString(l) {
			continue
		}
		return l, 0.9
	}
	return "", 0.1
}

func findAmount(t string, v2 bool) (string, float64) {
	if v2 {
		if ms := reLabeled.FindAllStringSubmatch(t, -1); len(ms) > 0 {
			return strings.TrimRight(ms[len(ms)-1][1], ".,"), 0.95
		}
	} else if m := reAnyTotal.FindStringSubmatch(t); m != nil {
		return strings.TrimRight(m[1], ".,"), 0.9
	}
	best, bestV := "", -1.0
	for _, m := range reMoney.FindAllString(t, -1) {
		if v, err := strconv.ParseFloat(strings.Replace(m, ",", ".", 1), 64); err == nil && v > bestV {
			best, bestV = m, v
		}
	}
	if best == "" {
		return "", 0.1
	}
	return best, 0.5
}

func findCurrency(t string, v2 bool) (string, float64) {
	if !v2 {
		return "USD", 0.9
	}
	for _, p := range []struct {
		re   *regexp.Regexp
		code string
	}{
		{reEUR, "EUR"}, {reGBP, "GBP"}, {reCAD, "CAD"}, {reAUD, "AUD"}, {reJPY, "JPY"}, {reCHF, "CHF"}, {reUSD, "USD"},
	} {
		if p.re.MatchString(t) {
			return p.code, 0.95
		}
	}
	if strings.Contains(t, "$") {
		return "USD", 0.85
	}
	return "USD", 0.4
}

func findTax(t string) (string, bool) {
	line := reTaxLine.FindString(t)
	if line == "" {
		return "", false
	}
	nums := reNumber.FindAllStringIndex(line, -1)
	for i := len(nums) - 1; i >= 0; i-- {
		end := nums[i][1]
		if end < len(line) && line[end] == '%' {
			continue
		}
		return line[nums[i][0]:end], true
	}
	return "", false
}

func findAttendees(t string) []string {
	m := reGuests.FindStringSubmatch(t)
	if m == nil {
		return nil
	}
	var out []string
	for _, n := range reSplit.Split(strings.TrimSpace(m[1]), -1) {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func pad(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

func findDate(t string, v2 bool) (string, float64) {
	if m := reISO.FindStringSubmatch(t); m != nil {
		return m[0], 0.95
	}
	if m := reSlash.FindStringSubmatch(t); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		switch {
		case a > 12:
			return m[3] + "-" + pad(m[2]) + "-" + pad(m[1]), 0.9
		case b > 12:
			return m[3] + "-" + pad(m[1]) + "-" + pad(m[2]), 0.95
		}
		return m[3] + "-" + pad(m[1]) + "-" + pad(m[2]), 0.5 // ambiguous: assume month/day
	}
	if !v2 {
		return "", 0.2
	}
	if m := reDots.FindStringSubmatch(t); m != nil {
		return m[3] + "-" + pad(m[2]) + "-" + pad(m[1]), 0.9
	}
	if m := reMDY.FindStringSubmatch(t); m != nil {
		return m[3] + "-" + months[strings.ToLower(m[1][:3])] + "-" + pad(m[2]), 0.95
	}
	if m := reDMY.FindStringSubmatch(t); m != nil {
		return m[3] + "-" + months[strings.ToLower(m[2][:3])] + "-" + pad(m[1]), 0.95
	}
	return "", 0.2
}

var catRules = []struct {
	cat string
	kw  []string
}{
	{"airfare", []string{"airline", "airways", "flight", "boarding", "e-ticket", "air lines"}},
	{"lodging", []string{"hotel", "inn ", "lodge", "room charge", "nights", "suites", "resort"}},
	{"software", []string{"subscription", "license", "saas", "cloud", "api usage", "hosting", "seats"}},
	{"transport", []string{"taxi", "uber", "lyft", "rideshare", "parking", "fuel", "gas station", "railway", "metro", "toll", "car rental"}},
	{"office_supplies", []string{"staples", "toner", "stationery", "office depot", "notebooks", "printer paper"}},
	{"entertainment", []string{"theater", "theatre", "concert", "museum", "bowling", "tickets"}},
	{"meals", []string{"cafe", "restaurant", "grill", "bistro", "diner", "coffee", "pizza", "kitchen", "latte", "lunch", "dinner", "breakfast", "server:"}},
}

func findCategory(t string, v2 bool) (string, float64) {
	l := strings.ToLower(t)
	for _, r := range catRules {
		if !v2 && r.cat != "meals" && r.cat != "transport" && r.cat != "lodging" {
			continue // v1 knows three categories
		}
		hits := 0
		for _, k := range r.kw {
			if strings.Contains(l, k) {
				hits++
			}
		}
		if hits >= 2 {
			return r.cat, 0.92
		}
		if hits == 1 {
			return r.cat, 0.65
		}
	}
	if v2 {
		return "other", 0.4
	}
	return "other", 0.85
}

func findPayment(t string) (string, float64) {
	switch {
	case reCard.MatchString(t):
		return "card", 0.9
	case reCash.MatchString(t):
		return "cash", 0.9
	case reOther.MatchString(t):
		return "other", 0.8
	}
	return "unknown", 0.6
}

func findDocType(t string) string {
	if reInvoice.MatchString(t) {
		return "invoice"
	}
	if reSlip.MatchString(t) {
		items := 0
		for _, l := range strings.Split(t, "\n") {
			if reMoney.MatchString(l) && !reLineSkip.MatchString(l) {
				items++
			}
		}
		if items == 0 {
			return "card_slip"
		}
	}
	return "itemized_receipt"
}

func hasAlcohol(t string, v2 bool) bool {
	if v2 {
		return reAlcV2.MatchString(t)
	}
	return reAlcV1.MatchString(t)
}
