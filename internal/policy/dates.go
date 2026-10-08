package policy

import (
	"bytes"
	"io"
	"slices"
	"time"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/expense"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func (p *Policy) dateRules(rec expense.Record, today string, add func(rule, sev, format string, a ...any)) {
	d, err := time.Parse("2006-01-02", rec.Date)
	if err != nil {
		return
	}
	if wd := d.Weekday(); (wd == time.Saturday || wd == time.Sunday) && slices.Contains(p.WeekendCategories, rec.Category) {
		add("WEEKEND", expense.DecisionWarn, "%s expense on a %s needs a business justification", rec.Category, wd)
	}
	if t, err := time.Parse("2006-01-02", today); err == nil && p.MaxAgeDays > 0 {
		if age := int(t.Sub(d).Hours() / 24); age > p.MaxAgeDays {
			add("EXPENSE_TOO_OLD", expense.DecisionViolation, "expense is %d days old, limit is %d", age, p.MaxAgeDays)
		}
	}
}
