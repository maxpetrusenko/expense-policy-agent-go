package expense

import "testing"

func TestParseMoney(t *testing.T) {
	ok := map[string]int64{
		"42.50": 4250, "$42.50": 4250, "USD 1,234.50": 123450, "1.234,50": 123450,
		"12,40": 1240, "EUR 12,40": 1240, "1,234": 123400, "7": 700, ".99": 99,
		"$ 0.05": 5, "(15.00)": -1500, "-3.10": -310, "1,234,567.89": 123456789,
	}
	for in, want := range ok {
		got, err := ParseMoney(in)
		if err != nil || got != want {
			t.Errorf("%q: got %d err %v want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "$", "1.234", "12.3456", "99999999999999.00"} {
		if in == "1.234" {
			continue // valid thousands form, asserted below
		}
		if _, err := ParseMoney(in); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
	if v, _ := ParseMoney("1.234"); v != 123400 {
		t.Errorf("1.234 => %d", v)
	}
}

func TestFormatMoney(t *testing.T) {
	if FormatMoney(123450) != "1234.50" || FormatMoney(-5) != "-0.05" {
		t.Fatal("format")
	}
}
