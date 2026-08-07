package duit

import (
	"bytes"
	"testing"
)

// FuzzNewFromString checks that parsing never panics and that any value
// that parses survives a String() round-trip unchanged.
func FuzzNewFromString(f *testing.F) {
	for _, seed := range []string{
		"0", "1.15", "-115000", "0.0001", "1e5", "-1.15e-4",
		"9999999999999999.9999", "abc", "", ".", "-", "1..2", "NaN", "Inf",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m, err := NewFromString(s)
		if err != nil {
			return
		}
		back, err := NewFromString(m.String())
		if err != nil {
			t.Fatalf("round-trip parse of %q (from %q) failed: %v", m.String(), s, err)
		}
		if !back.Equal(m) {
			t.Fatalf("round-trip of %q changed value: %s != %s", s, back, m)
		}
	})
}

// FuzzUnmarshalJSON checks that arbitrary JSON tokens never panic and that
// accepted values marshal back to a parseable, equal value.
func FuzzUnmarshalJSON(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`"1.15"`), []byte(`1.15`), []byte(`null`), []byte(`"null"`),
		[]byte(`""`), []byte(`"-0.0001"`), []byte(`{"a":1}`), []byte(`[1]`),
		[]byte(`"1e10"`), []byte(`--1`), []byte(`"`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var m Money
		if err := m.UnmarshalJSON(data); err != nil {
			return
		}
		out, err := m.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON after accepting %q: %v", data, err)
		}
		var back Money
		if err := back.UnmarshalJSON(out); err != nil {
			t.Fatalf("re-unmarshal of %q (from %q): %v", out, data, err)
		}
		if !back.Equal(m) {
			t.Fatalf("round-trip of %q changed value: %s != %s", data, back, m)
		}
	})
}

// FuzzScanBytes checks that scanning arbitrary driver bytes never panics
// and that accepted values survive a Value() round-trip at ScaleDB.
func FuzzScanBytes(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("1234.5000"), []byte("-0.0001"), []byte(""), []byte("x"),
		[]byte("1e4"), []byte("999999999999999.9999"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var m Money
		if err := m.Scan(data); err != nil {
			return
		}
		v, err := m.Value()
		if err != nil {
			t.Fatalf("Value() after scanning %q: %v", data, err)
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("Value() = %T, want string", v)
		}
		var back Money
		if err := back.Scan([]byte(s)); err != nil {
			t.Fatalf("re-scan of %q (from %q): %v", s, data, err)
		}
		if !back.Equal(m.RoundDB()) {
			t.Fatalf("DB round-trip of %q changed value: %s != %s", data, back, m.RoundDB())
		}
	})
}

// FuzzAllocate checks the core invariant on random inputs: the parts sum
// back to exactly the rounded total, every part count matches, and
// adjacent parts differ by at most one smallest unit.
func FuzzAllocate(f *testing.F) {
	f.Add(int64(100), uint8(0), uint8(3))
	f.Add(int64(-999999999), uint8(4), uint8(7))
	f.Add(int64(1), uint8(4), uint8(50))
	f.Fuzz(func(t *testing.T, units int64, scaleRaw, nRaw uint8) {
		scale := int32(scaleRaw % 7) // 0..6
		n := int(nRaw%50) + 1        // 1..50
		m := moneyFromUnits(units, scale)

		parts, err := m.Allocate(n, scale)
		if err != nil {
			t.Fatalf("Allocate(%d, %d) on %s: %v", n, scale, m, err)
		}
		if len(parts) != n {
			t.Fatalf("got %d parts, want %d", len(parts), n)
		}
		if !Sum(parts[0], parts[1:]...).Equal(m.Round(scale)) {
			t.Fatalf("parts of %s do not sum back: %v", m, parts)
		}
		spread := Max(parts[0], parts[1:]...).Sub(Min(parts[0], parts[1:]...))
		if spread.GreaterThan(unitAt(scale)) {
			t.Fatalf("parts of %s spread more than one unit: %v", m, parts)
		}
	})
}

// FuzzAllocateRatios checks the proportional invariant: parts always sum
// back to the rounded total and zero ratios always yield zero parts.
func FuzzAllocateRatios(f *testing.F) {
	f.Add(int64(100), uint8(0), int64(70), int64(30), int64(0))
	f.Add(int64(-12345), uint8(4), int64(1), int64(2), int64(3))
	f.Fuzz(func(t *testing.T, units int64, scaleRaw uint8, r1, r2, r3 int64) {
		scale := int32(scaleRaw % 7)
		ratios := []int64{clampRatio(r1), clampRatio(r2), clampRatio(r3)}
		if ratios[0]+ratios[1]+ratios[2] == 0 {
			ratios[0] = 1
		}
		m := moneyFromUnits(units, scale)

		parts, err := m.AllocateRatios(scale, ratios...)
		if err != nil {
			t.Fatalf("AllocateRatios(%d, %v) on %s: %v", scale, ratios, m, err)
		}
		if !Sum(parts[0], parts[1:]...).Equal(m.Round(scale)) {
			t.Fatalf("parts of %s at %v do not sum back: %v", m, ratios, parts)
		}
		for i, r := range ratios {
			if r == 0 && !parts[i].IsZero() {
				t.Fatalf("ratio 0 received %s (parts %v)", parts[i], parts)
			}
		}
	})
}

func clampRatio(r int64) int64 {
	if r < 0 {
		r = -r
	}
	return r % 1_000_000
}

// moneyFromUnits builds an amount of `units` smallest units at the given
// scale, e.g. (12345, 4) -> 1.2345.
func moneyFromUnits(units int64, scale int32) Money {
	return Money{NewFromInt(units).d.Shift(-scale)}
}

// unitAt returns one smallest unit at the given scale, e.g. 0.0001 at 4.
func unitAt(scale int32) Money {
	return moneyFromUnits(1, scale)
}

// FuzzJSONBytesEqualsGoString guards MarshalText/MarshalJSON consistency.
func FuzzJSONBytesEqualsGoString(f *testing.F) {
	f.Add("1.15")
	f.Add("-99999.0001")
	f.Fuzz(func(t *testing.T, s string) {
		m, err := NewFromString(s)
		if err != nil {
			return
		}
		txt, err := m.MarshalText()
		if err != nil {
			t.Fatal(err)
		}
		jsn, err := m.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if want := append(append([]byte(`"`), txt...), '"'); !bytes.Equal(jsn, want) {
			t.Fatalf("MarshalJSON %q inconsistent with MarshalText %q", jsn, txt)
		}
	})
}
