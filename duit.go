// Package duit provides a decimal-backed Money type for safe monetary
// arithmetic, intended as the single place where rounding policy lives.
//
// It is designed for gradual migration away from float64:
//
//   - The database column stays PostgreSQL NUMERIC(19,4); Money implements
//     sql.Scanner and driver.Valuer and always persists at ScaleDB.
//   - Existing models/viewmodels can keep float64 for now; convert at the
//     boundary with FromFloat / ToFloat (both deprecated on purpose so the
//     remaining call sites are easy to find and burn down).
//   - JSON marshals as a string ("115000") so precision survives transport;
//     unmarshaling accepts both a string and a bare JSON number so old
//     clients keep working during the migration.
//
// All rounding is half-up (ties away from zero) and only happens at explicit
// points: Round/RoundDB/RoundIDR, Div (scale is mandatory), PercentOf,
// Allocate, and Value (DB write). Add/Sub/Mul are exact.
package duit

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"

	"github.com/shopspring/decimal"
)

const (
	// ScaleDB is the scale used for persistence, matching NUMERIC(19,4).
	ScaleDB int32 = 4
	// ScaleIDR is the scale for user-facing rupiah amounts (no subunits).
	ScaleIDR int32 = 0
)

var (
	// ErrDivideByZero is returned by Div when the divisor is zero.
	ErrDivideByZero = errors.New("duit: division by zero")
	// ErrInvalidParts is returned by Allocate when n < 1.
	ErrInvalidParts = errors.New("duit: number of parts must be >= 1")
	// ErrInvalidRatios is returned by AllocateRatios when no ratios are
	// given, a ratio is negative, or all ratios are zero.
	ErrInvalidRatios = errors.New("duit: ratios must be non-negative with a positive sum")
	// ErrScanNull is returned when scanning SQL NULL into Money; use
	// NullMoney for nullable columns.
	ErrScanNull = errors.New("duit: cannot scan NULL into Money, use NullMoney")
)

// Money is an immutable, arbitrary-precision monetary amount.
// The zero value is 0 and ready to use.
type Money struct {
	d decimal.Decimal
}

// Zero returns Money worth 0.
func Zero() Money { return Money{} }

// NewFromInt returns Money worth v whole units (e.g. rupiah).
func NewFromInt(v int64) Money { return Money{decimal.NewFromInt(v)} }

// NewFromDecimal wraps an existing decimal.Decimal.
//
// Together with Decimal, this is the only public API tied to the backing
// shopspring/decimal library — everything else speaks Money, so the
// backend can be swapped without breaking callers. Prefer NewFromString /
// NewFromInt unless interoperating with code that already uses decimal.
func NewFromDecimal(d decimal.Decimal) Money { return Money{d} }

// NewFromString parses a decimal string such as "1.15" or "-115000".
func NewFromString(s string) (Money, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return Money{}, fmt.Errorf("duit: parse %q: %w", s, err)
	}
	return Money{d}, nil
}

// MustFromString is NewFromString that panics on error; for constants and tests.
func MustFromString(s string) Money {
	m, err := NewFromString(s)
	if err != nil {
		panic(err)
	}
	return m
}

// FromFloat converts a float64 into Money using the shortest decimal
// representation of f (so FromFloat(1.15) is exactly 1.15).
// It panics if f is NaN or ±Inf — non-finite values can only come from a
// broken calculation and must never become money.
//
// Deprecated: only for the boundary with legacy float64 models/viewmodels.
// New code must construct Money from string, int, or decimal. Remove the
// call site once its model field is migrated to Money.
func FromFloat(f float64) Money { return Money{decimal.NewFromFloat(f)} }

// ToFloat converts Money back to float64.
//
// Deprecated: only for the boundary with legacy float64 models/viewmodels.
// The conversion may lose precision; never feed the result back into
// monetary arithmetic. Remove the call site once its model field is
// migrated to Money.
func (m Money) ToFloat() float64 {
	f, _ := m.d.Float64()
	return f
}

// Decimal returns the underlying decimal.Decimal. Like NewFromDecimal,
// this is an interop escape hatch — see the note there.
func (m Money) Decimal() decimal.Decimal { return m.d }

// --- exact arithmetic ---

// Add returns m + o. Exact, no rounding.
func (m Money) Add(o Money) Money { return Money{m.d.Add(o.d)} }

// Sub returns m - o. Exact, no rounding.
func (m Money) Sub(o Money) Money { return Money{m.d.Sub(o.d)} }

// Mul returns m * factor. Exact, no rounding; round at the boundary
// (RoundDB/RoundIDR) when the result leaves calculation code.
// Non-integer factors (rates, kurs) are constructed with NewFromString.
func (m Money) Mul(factor Money) Money { return Money{m.d.Mul(factor.d)} }

// MulInt returns m * n. Exact, no rounding.
func (m Money) MulInt(n int64) Money { return m.Mul(NewFromInt(n)) }

// Neg returns -m.
func (m Money) Neg() Money { return Money{m.d.Neg()} }

// Abs returns |m|.
func (m Money) Abs() Money { return Money{m.d.Abs()} }

// Sum returns m + the given amounts. Exact, no rounding.
func Sum(first Money, rest ...Money) Money {
	total := first.d
	for _, m := range rest {
		total = total.Add(m.d)
	}
	return Money{total}
}

// --- rounding: the only places precision is dropped ---

// Round rounds half-up (ties away from zero) to the given scale:
// Round(0) of 2.5 is 3, of -2.5 is -3.
func (m Money) Round(scale int32) Money { return Money{m.d.Round(scale)} }

// RoundDB rounds to ScaleDB, the persistence scale of NUMERIC(19,4).
func (m Money) RoundDB() Money { return m.Round(ScaleDB) }

// RoundIDR rounds to ScaleIDR, i.e. whole rupiah.
func (m Money) RoundIDR() Money { return m.Round(ScaleIDR) }

// RoundDown truncates toward zero at the given scale: 2.9 -> 2, -2.9 -> -2.
// Indonesian tax invoices (faktur pajak) commonly require rounding VAT
// down to whole rupiah; that is RoundDown(ScaleIDR).
func (m Money) RoundDown(scale int32) Money { return Money{m.d.RoundDown(scale)} }

// RoundUp rounds away from zero at the given scale: 2.1 -> 3, -2.1 -> -3.
func (m Money) RoundUp(scale int32) Money { return Money{m.d.RoundUp(scale)} }

// RoundBank rounds half to even (banker's rounding) at the given scale:
// 2.5 -> 2, 3.5 -> 4. Use when a spec explicitly demands unbiased
// rounding; the library default everywhere else is half-up.
func (m Money) RoundBank(scale int32) Money { return Money{m.d.RoundBank(scale)} }

// Div divides m by divisor, rounding half-up at the given scale.
// The scale is mandatory: division is the operation that silently created
// float64 drift, so every call site must state the precision it wants
// (usually ScaleDB inside calculations, ScaleIDR at the user boundary).
func (m Money) Div(divisor Money, scale int32) (Money, error) {
	if divisor.IsZero() {
		return Money{}, ErrDivideByZero
	}
	return Money{m.d.DivRound(divisor.d, scale)}, nil
}

// DivInt is Div with an integer divisor.
func (m Money) DivInt(n int64, scale int32) (Money, error) {
	return m.Div(NewFromInt(n), scale)
}

// PercentOf returns percent% of m, rounded half-up at the given scale.
// The multiplication and the /100 are exact; rounding happens once, at the end.
//
//	tax := price.PercentOf(duit.NewFromInt(11), duit.ScaleDB) // PPN 11%
func (m Money) PercentOf(percent Money, scale int32) Money {
	return Money{m.d.Mul(percent.d).Shift(-2).Round(scale)}
}

// Allocate splits m into n parts at the given scale such that the parts sum
// back to exactly m.Round(scale). The remainder is distributed one smallest
// unit (10^-scale) at a time, starting from the first part, so earlier parts
// may be one unit larger:
//
//	MustFromString("100").Allocate(3, ScaleIDR) // [34 33 33]
//	MustFromString("100").Allocate(3, ScaleDB)  // [33.3334 33.3333 33.3333]
//
// For negative amounts the same distribution is applied to the absolute
// value and then negated, so -100 into 3 is [-34 -33 -33].
func (m Money) Allocate(n int, scale int32) ([]Money, error) {
	if n < 1 {
		return nil, ErrInvalidParts
	}
	rounded := m.Round(scale)
	negative := rounded.d.IsNegative()

	// Work in integer smallest units (big.Int: NUMERIC(19,4) can exceed int64).
	units := rounded.d.Abs().Shift(scale).BigInt()
	quo, rem := new(big.Int).QuoRem(units, big.NewInt(int64(n)), new(big.Int))

	parts := make([]Money, n)
	one := big.NewInt(1)
	for i := range parts {
		u := new(big.Int).Set(quo)
		if int64(i) < rem.Int64() {
			u.Add(u, one)
		}
		d := decimal.NewFromBigInt(u, -scale)
		if negative {
			d = d.Neg()
		}
		parts[i] = Money{d}
	}
	return parts, nil
}

// AllocateRatios splits m proportionally to the given non-negative ratios
// at the given scale, such that the parts sum back to exactly
// m.Round(scale). Distribution is largest-remainder: each part first gets
// its exact proportional share truncated to whole smallest units, then the
// leftover units go one at a time to the parts with the largest
// fractional remainders (ties broken by lower index):
//
//	MustFromString("100").AllocateRatios(ScaleIDR, 70, 30) // [70 30]
//	MustFromString("100").AllocateRatios(ScaleIDR, 1, 1, 1) // [34 33 33]
//
// A part with ratio 0 always receives exactly 0. For negative amounts the
// distribution is applied to the absolute value and then negated.
func (m Money) AllocateRatios(scale int32, ratios ...int64) ([]Money, error) {
	if len(ratios) == 0 {
		return nil, ErrInvalidRatios
	}
	ratioSum := new(big.Int)
	for _, r := range ratios {
		if r < 0 {
			return nil, ErrInvalidRatios
		}
		ratioSum.Add(ratioSum, big.NewInt(r))
	}
	if ratioSum.Sign() == 0 {
		return nil, ErrInvalidRatios
	}

	rounded := m.Round(scale)
	negative := rounded.d.IsNegative()
	total := rounded.d.Abs().Shift(scale).BigInt()

	units := make([]*big.Int, len(ratios))
	remainders := make([]struct {
		idx int
		rem *big.Int
	}, len(ratios))
	assigned := new(big.Int)
	for i, r := range ratios {
		num := new(big.Int).Mul(total, big.NewInt(r))
		q, rem := new(big.Int).QuoRem(num, ratioSum, new(big.Int))
		units[i] = q
		remainders[i].idx = i
		remainders[i].rem = rem
		assigned.Add(assigned, q)
	}

	// 0 <= leftover < len(ratios), and only parts with a non-zero
	// fractional remainder (hence non-zero ratio) can receive a unit.
	leftover := new(big.Int).Sub(total, assigned).Int64()
	sort.SliceStable(remainders, func(a, b int) bool {
		return remainders[a].rem.Cmp(remainders[b].rem) > 0
	})
	one := big.NewInt(1)
	for i := int64(0); i < leftover; i++ {
		u := units[remainders[i].idx]
		u.Add(u, one)
	}

	parts := make([]Money, len(ratios))
	for i, u := range units {
		d := decimal.NewFromBigInt(u, -scale)
		if negative {
			d = d.Neg()
		}
		parts[i] = Money{d}
	}
	return parts, nil
}

// --- comparison ---

// Cmp returns -1 if m < o, 0 if equal, +1 if m > o.
func (m Money) Cmp(o Money) int { return m.d.Cmp(o.d) }

// Equal reports whether m and o represent the same amount (1.5 equals 1.50).
func (m Money) Equal(o Money) bool { return m.d.Equal(o.d) }

// IsZero reports whether m is 0.
func (m Money) IsZero() bool { return m.d.IsZero() }

// IsNegative reports whether m < 0.
func (m Money) IsNegative() bool { return m.d.IsNegative() }

// IsPositive reports whether m > 0.
func (m Money) IsPositive() bool { return m.d.IsPositive() }

// LessThan reports whether m < o.
func (m Money) LessThan(o Money) bool { return m.d.LessThan(o.d) }

// LessThanOrEqual reports whether m <= o.
func (m Money) LessThanOrEqual(o Money) bool { return m.d.LessThanOrEqual(o.d) }

// GreaterThan reports whether m > o.
func (m Money) GreaterThan(o Money) bool { return m.d.GreaterThan(o.d) }

// GreaterThanOrEqual reports whether m >= o.
func (m Money) GreaterThanOrEqual(o Money) bool { return m.d.GreaterThanOrEqual(o.d) }

// Min returns the smallest of the given amounts.
func Min(first Money, rest ...Money) Money {
	minimum := first
	for _, m := range rest {
		if m.LessThan(minimum) {
			minimum = m
		}
	}
	return minimum
}

// Max returns the largest of the given amounts.
func Max(first Money, rest ...Money) Money {
	maximum := first
	for _, m := range rest {
		if m.GreaterThan(maximum) {
			maximum = m
		}
	}
	return maximum
}

// --- formatting ---

// String returns the amount without trailing zeros, e.g. "115000.5".
func (m Money) String() string { return m.d.String() }

// StringFixed returns the amount with exactly the given number of decimal
// places, e.g. StringFixed(ScaleDB) -> "115000.5000".
func (m Money) StringFixed(scale int32) string { return m.d.StringFixed(scale) }

// FormatIDR renders the amount as whole rupiah with dot thousand
// separators: "Rp1.665.000", or "-Rp55.000" for negative amounts.
// The amount is rounded half-up to ScaleIDR first.
func (m Money) FormatIDR() string {
	rounded := m.RoundIDR()
	digits := rounded.d.Abs().String()

	var b strings.Builder
	if rounded.IsNegative() {
		b.WriteByte('-')
	}
	b.WriteString("Rp")
	n := len(digits)
	for i := 0; i < n; i++ {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteByte(digits[i])
	}
	return b.String()
}

// --- database/sql: NUMERIC(19,4) <-> Money ---

// Value implements driver.Valuer. It always writes at ScaleDB (rounding
// half-up if the amount carries more precision), so the DB representation
// is canonical regardless of how the amount was computed.
func (m Money) Value() (driver.Value, error) {
	return m.RoundDB().StringFixed(ScaleDB), nil
}

// Scan implements sql.Scanner for NUMERIC columns. It accepts the string
// and []byte forms produced by pq/pgx, plus int64 and float64 for drivers
// or legacy columns that deliver numbers. NULL is an error; use NullMoney
// for nullable columns.
func (m *Money) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		return ErrScanNull
	case []byte:
		d, err := decimal.NewFromString(string(v))
		if err != nil {
			return fmt.Errorf("duit: scan %q: %w", v, err)
		}
		m.d = d
	case string:
		d, err := decimal.NewFromString(v)
		if err != nil {
			return fmt.Errorf("duit: scan %q: %w", v, err)
		}
		m.d = d
	case int64:
		m.d = decimal.NewFromInt(v)
	case float64:
		// Legacy path: a float column or a driver decoding NUMERIC as float.
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("duit: cannot scan non-finite float %v into Money", v)
		}
		m.d = decimal.NewFromFloat(v)
	default:
		return fmt.Errorf("duit: cannot scan %T into Money", src)
	}
	return nil
}

// NullMoney is Money for nullable NUMERIC columns, analogous to sql.NullInt64.
type NullMoney struct {
	Money Money
	Valid bool
}

// Value implements driver.Valuer.
func (n NullMoney) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Money.Value()
}

// Scan implements sql.Scanner.
func (n *NullMoney) Scan(src any) error {
	if src == nil {
		*n = NullMoney{}
		return nil
	}
	n.Valid = true
	return n.Money.Scan(src)
}

// --- JSON ---

// MarshalJSON encodes the amount as a JSON string ("115000.5") so that
// precision survives clients that parse JSON numbers as float64.
func (m Money) MarshalJSON() ([]byte, error) {
	return []byte(`"` + m.d.String() + `"`), nil
}

// UnmarshalJSON accepts both a JSON string ("1.15") and, for compatibility
// with clients not yet migrated, a bare JSON number (1.15). The raw token
// is parsed as a decimal, so 1.15 arrives as exactly 1.15, never
// 1.14999999999999. JSON null leaves the value unchanged.
func (m *Money) UnmarshalJSON(data []byte) error {
	s := string(data)
	if s == "null" {
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return fmt.Errorf("duit: unmarshal %s: %w", data, err)
	}
	m.d = d
	return nil
}

// MarshalText implements encoding.TextMarshaler, so Money works as a JSON
// map key and with text-based encoders (XML, TOML, ...).
func (m Money) MarshalText() ([]byte, error) {
	return []byte(m.d.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *Money) UnmarshalText(text []byte) error {
	d, err := decimal.NewFromString(string(text))
	if err != nil {
		return fmt.Errorf("duit: unmarshal text %q: %w", text, err)
	}
	m.d = d
	return nil
}
