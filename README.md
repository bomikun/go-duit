# go-duit

[![Go Reference](https://pkg.go.dev/badge/github.com/bomikun/go-duit.svg)](https://pkg.go.dev/github.com/bomikun/go-duit)
[![CI](https://github.com/bomikun/go-duit/actions/workflows/ci.yml/badge.svg)](https://github.com/bomikun/go-duit/actions/workflows/ci.yml)

Safe money arithmetic for Go — a decimal-backed `Money` type with explicit rounding, allocation, and first-class IDR support. Built on [`shopspring/decimal`](https://github.com/shopspring/decimal), designed for **gradual migration** away from `float64`.

The backing decimal library is an implementation detail: the entire public API speaks `Money` (construct rates with `NewFromString`), except the `NewFromDecimal`/`Decimal` interop escape hatch. Swapping the backend later would not break callers.

```go
f := 1.15
fmt.Println(f * 100) // 114.99999999999999  ← the bug you already have

fmt.Println(duit.MustFromString("1.15").MulInt(100)) // 115
```

## Install

```
go get github.com/bomikun/go-duit
```

```go
import duit "github.com/bomikun/go-duit"
```

## Quick start

```go
price := duit.NewFromInt(1_500_000)

ppn := price.PercentOf(duit.NewFromInt(11), duit.ScaleIDR) // 165000 (PPN 11%)
total := price.Add(ppn)                                       // 1665000

parts, _ := total.Allocate(3, duit.ScaleIDR)  // [555000 555000 555000], sums back exactly
third, _ := duit.NewFromInt(1).DivInt(3, duit.ScaleDB) // 0.3333 — scale is always explicit
```

## Design

- **Exact by default.** `Add` / `Sub` / `Mul` / `MulInt` never round.
- **Rounding is explicit and centralized** — always half-up (ties away from zero), and only at these points:
  - `Div(divisor, scale)` / `DivInt(n, scale)` — the scale argument is **mandatory**; division is where float64 drift is born, so every call site states the precision it wants.
  - `PercentOf(pct, scale)` — exact multiply, a single rounding at the end.
  - `Round(scale)`, `RoundDB()`, `RoundIDR()`.
  - `RoundDown` / `RoundUp` / `RoundBank` — explicitly named modes for specs that demand them (e.g. Indonesian tax invoices round VAT **down**: `vat.RoundDown(duit.ScaleIDR)`).
  - `Value()` — database writes are canonicalized to `ScaleDB`.
- **`Allocate(n, scale)` never loses a unit.** Largest-remainder split: `100 → [34 33 33]` at `ScaleIDR`, `[33.3334 33.3333 33.3333]` at `ScaleDB` — parts always sum back exactly.
- **`AllocateRatios(scale, ratios...)`** splits proportionally with the same exact-sum guarantee: `100 → [70 30]` for `(70, 30)`; a ratio of 0 always receives exactly 0.
- Comparisons read like prose — `LessThan`, `GreaterThanOrEqual`, … — plus `duit.Min`/`duit.Max` for caps and floors.
- `FormatIDR()` renders `"Rp1.665.000"` (dot thousand separators, whole rupiah).
- **Scales as constants**: `ScaleDB = 4` (PostgreSQL `NUMERIC(19,4)`), `ScaleIDR = 0` (whole rupiah).
- The zero value of `Money` is `0` and ready to use.
- Parsing, JSON, SQL scanning, and the allocation invariants are **fuzz-tested**.

## Database (PostgreSQL `NUMERIC`)

`Money` implements `sql.Scanner` and `driver.Valuer`; `NullMoney` covers nullable columns. No manual conversion:

```go
var row struct {
    Amount duit.Money
    Fee    duit.NullMoney
}
err := db.QueryRow(`SELECT amount, fee FROM payments WHERE id=$1`, id).
    Scan(&row.Amount, &row.Fee)

_, err = db.Exec(`UPDATE payments SET amount=$1 WHERE id=$2`, row.Amount, id)
```

Writes always round half-up to `ScaleDB`, so the stored representation is canonical.

## JSON

`Money` marshals as a **string** (`"115000"`) so precision survives clients that parse JSON numbers as float64. Unmarshaling accepts both strings and bare numbers, so not-yet-migrated clients keep working:

```go
json.Marshal(struct{ Total duit.Money }{total}) // {"Total":"1665000"}
json.Unmarshal([]byte(`{"Total":1.15}`), &v)    // exactly 1.15, never 1.1499999...
```

## Migrating from float64

`FromFloat` and `ToFloat` exist only as the boundary with legacy `float64` models/viewmodels, and are **deprecated on purpose**: every remaining call site shows up in `staticcheck` (SA1019) so you can burn them down incrementally.

- Staged migration guide: [docs/MIGRATION.md](docs/MIGRATION.md)
- Runnable end-to-end example — legacy float64 model in, `Money` everywhere in the middle, float64 viewmodel out:

```
go run ./examples/migration
```

## License

[MIT](LICENSE)
