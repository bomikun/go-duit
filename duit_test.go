package duit

import (
	"database/sql"
	"database/sql/driver"
	"encoding"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/shopspring/decimal"
)

var (
	_ sql.Scanner              = (*Money)(nil)
	_ driver.Valuer            = Money{}
	_ sql.Scanner              = (*NullMoney)(nil)
	_ driver.Valuer            = NullMoney{}
	_ json.Marshaler           = Money{}
	_ json.Unmarshaler         = (*Money)(nil)
	_ encoding.TextMarshaler   = Money{}
	_ encoding.TextUnmarshaler = (*Money)(nil)
)

func mustEq(t *testing.T, got Money, want string) {
	t.Helper()
	if !got.Equal(MustFromString(want)) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// The motivating bug: with float64, 1.15 * 100 is 114.99999999999999.
// With Money it is exactly 115.
func Test_1_15_Times_100(t *testing.T) {
	f := 1.15
	if f*100 == 115.0 {
		t.Fatal("expected float64 to be broken here; test premise invalid")
	}

	got := MustFromString("1.15").MulInt(100)
	mustEq(t, got, "115")
	if s := got.String(); s != "115" {
		t.Fatalf("String() = %q, want %q", s, "115")
	}

	// Even coming from a legacy float64 field, FromFloat uses the shortest
	// decimal representation, so the boundary conversion stays exact.
	mustEq(t, FromFloat(1.15).MulInt(100), "115")
}

func TestDiv_OneThird(t *testing.T) {
	one := NewFromInt(1)

	got, err := one.DivInt(3, ScaleDB)
	if err != nil {
		t.Fatal(err)
	}
	mustEq(t, got, "0.3333")

	got, err = one.DivInt(3, ScaleIDR)
	if err != nil {
		t.Fatal(err)
	}
	mustEq(t, got, "0")

	// Half-up rounding at the requested scale: 2/3 = 0.66666... -> 0.6667.
	got, err = NewFromInt(2).DivInt(3, ScaleDB)
	if err != nil {
		t.Fatal(err)
	}
	mustEq(t, got, "0.6667")

	if _, err := one.Div(Zero(), ScaleDB); !errors.Is(err, ErrDivideByZero) {
		t.Fatalf("dividing by zero: got err %v, want ErrDivideByZero", err)
	}
}

func TestAllocate_100_Into_3(t *testing.T) {
	hundred := NewFromInt(100)

	t.Run("whole rupiah", func(t *testing.T) {
		parts, err := hundred.Allocate(3, ScaleIDR)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"34", "33", "33"}
		for i, w := range want {
			mustEq(t, parts[i], w)
		}
		mustEq(t, Sum(parts[0], parts[1:]...), "100")
	})

	t.Run("db scale", func(t *testing.T) {
		parts, err := hundred.Allocate(3, ScaleDB)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"33.3334", "33.3333", "33.3333"}
		for i, w := range want {
			mustEq(t, parts[i], w)
		}
		mustEq(t, Sum(parts[0], parts[1:]...), "100")
	})

	t.Run("negative amount", func(t *testing.T) {
		parts, err := NewFromInt(-100).Allocate(3, ScaleIDR)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"-34", "-33", "-33"}
		for i, w := range want {
			mustEq(t, parts[i], w)
		}
		mustEq(t, Sum(parts[0], parts[1:]...), "-100")
	})

	t.Run("no remainder", func(t *testing.T) {
		parts, err := NewFromInt(90).Allocate(3, ScaleIDR)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range parts {
			mustEq(t, p, "30")
		}
	})

	t.Run("invalid parts", func(t *testing.T) {
		if _, err := hundred.Allocate(0, ScaleIDR); !errors.Is(err, ErrInvalidParts) {
			t.Fatalf("got err %v, want ErrInvalidParts", err)
		}
	})
}

func TestPercentOf(t *testing.T) {
	price := NewFromInt(1_000_000)

	ppn := price.PercentOf(NewFromInt(11), ScaleDB) // PPN 11%
	mustEq(t, ppn, "110000")

	// 12.5% of 199 = 24.875 -> whole rupiah rounds half-up to 25.
	disc := NewFromInt(199).PercentOf(MustFromString("12.5"), ScaleIDR)
	mustEq(t, disc, "25")
}

func TestDecimalInterop(t *testing.T) {
	// The only decimal-typed API: the escape hatch for interop.
	m := NewFromDecimal(decimal.RequireFromString("1.15"))
	mustEq(t, m, "1.15")
	if got := m.Decimal(); !got.Equal(decimal.RequireFromString("1.15")) {
		t.Fatalf("Decimal() = %s, want 1.15", got)
	}
}

func TestRounding(t *testing.T) {
	mustEq(t, MustFromString("0.12345").RoundDB(), "0.1235")
	mustEq(t, MustFromString("1.5").RoundIDR(), "2")
	mustEq(t, MustFromString("0.5").RoundIDR(), "1")
	// Half-up means ties away from zero, also for negatives.
	mustEq(t, MustFromString("-0.5").RoundIDR(), "-1")
	mustEq(t, MustFromString("-0.12345").RoundDB(), "-0.1235")
}

func TestSQLValueAndScan(t *testing.T) {
	v, err := MustFromString("1234.5").Value()
	if err != nil {
		t.Fatal(err)
	}
	if v != "1234.5000" {
		t.Fatalf("Value() = %v, want %q (canonical NUMERIC(19,4) form)", v, "1234.5000")
	}

	// Extra precision is rounded half-up at the DB boundary, centrally.
	v, err = MustFromString("0.12345").Value()
	if err != nil {
		t.Fatal(err)
	}
	if v != "0.1235" {
		t.Fatalf("Value() = %v, want %q", v, "0.1235")
	}

	for _, src := range []any{[]byte("1234.5000"), "1234.5000"} {
		var m Money
		if err := m.Scan(src); err != nil {
			t.Fatal(err)
		}
		mustEq(t, m, "1234.5")
	}

	var m Money
	if err := m.Scan(int64(115)); err != nil {
		t.Fatal(err)
	}
	mustEq(t, m, "115")

	if err := m.Scan(1.15); err != nil {
		t.Fatal(err)
	}
	mustEq(t, m, "1.15")

	if err := m.Scan(nil); !errors.Is(err, ErrScanNull) {
		t.Fatalf("Scan(nil): got err %v, want ErrScanNull", err)
	}
	if err := m.Scan(true); err == nil {
		t.Fatal("Scan(bool): want error, got nil")
	}

	// Non-finite floats must error, never panic.
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if err := m.Scan(f); err == nil {
			t.Fatalf("Scan(%v): want error, got nil", f)
		}
	}
}

func TestNullMoney(t *testing.T) {
	var n NullMoney
	if err := n.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if n.Valid {
		t.Fatal("Scan(nil): Valid = true, want false")
	}
	if v, err := n.Value(); err != nil || v != nil {
		t.Fatalf("invalid NullMoney.Value() = (%v, %v), want (nil, nil)", v, err)
	}

	if err := n.Scan([]byte("99.9900")); err != nil {
		t.Fatal(err)
	}
	if !n.Valid {
		t.Fatal("Valid = false after scanning a value")
	}
	mustEq(t, n.Money, "99.99")
}

func TestJSON(t *testing.T) {
	type payload struct {
		Amount Money `json:"amount"`
	}

	out, err := json.Marshal(payload{Amount: MustFromString("1.15").MulInt(100)})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"amount":"115"}` {
		t.Fatalf("Marshal = %s, want %s", out, `{"amount":"115"}`)
	}

	// String form (new clients) and bare number form (legacy clients) both work.
	for _, in := range []string{`{"amount":"1.15"}`, `{"amount":1.15}`} {
		var p payload
		if err := json.Unmarshal([]byte(in), &p); err != nil {
			t.Fatalf("Unmarshal %s: %v", in, err)
		}
		mustEq(t, p.Amount, "1.15")
	}

	var p payload
	if err := json.Unmarshal([]byte(`{"amount":"abc"}`), &p); err == nil {
		t.Fatal("Unmarshal invalid amount: want error, got nil")
	}

	// JSON null leaves the value unchanged; the string "null" is invalid.
	p.Amount = NewFromInt(7)
	if err := json.Unmarshal([]byte(`{"amount":null}`), &p); err != nil {
		t.Fatal(err)
	}
	mustEq(t, p.Amount, "7")
	if err := json.Unmarshal([]byte(`{"amount":"null"}`), &p); err == nil {
		t.Fatal(`Unmarshal "null" string: want error, got nil`)
	}
}

func TestTextMarshaling(t *testing.T) {
	// TextMarshaler makes Money usable as a JSON map key.
	prices := map[Money]string{MustFromString("1.15"): "promo"}
	out, err := json.Marshal(prices)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"1.15":"promo"}` {
		t.Fatalf("map key marshal = %s, want %s", out, `{"1.15":"promo"}`)
	}

	var m Money
	if err := m.UnmarshalText([]byte("115000.5")); err != nil {
		t.Fatal(err)
	}
	mustEq(t, m, "115000.5")
	if err := m.UnmarshalText([]byte("abc")); err == nil {
		t.Fatal("UnmarshalText invalid: want error, got nil")
	}
}

func TestFloatBoundary(t *testing.T) {
	m := MustFromString("115000.5")
	if got := m.ToFloat(); got != 115000.5 {
		t.Fatalf("ToFloat() = %v, want 115000.5", got)
	}
	mustEq(t, FromFloat(115000.5), "115000.5")
}

func TestZeroValueIsUsable(t *testing.T) {
	var m Money
	if !m.IsZero() {
		t.Fatal("zero value should be 0")
	}
	mustEq(t, m.Add(NewFromInt(5)), "5")
}
