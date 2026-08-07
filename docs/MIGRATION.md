# Migration Guide: float64 → `duit.Money` (incremental)

This document explains how to move money handling from `float64` to
`duit.Money` (backed by `shopspring/decimal`) **without a big-bang
rewrite**. Existing models and viewmodels that still use `float64` do not
need to change at the start.

The core principle: **float64 may only live at the boundaries**, and every
conversion goes through `duit.FromFloat` / `duit.ToFloat`, which are
deliberately marked `Deprecated` — so every remaining usage stays visible
in `staticcheck` (SA1019) and can be burned down sprint by sprint.

```
handler / viewmodel (float64)           ── Stage 4: migrated last
        │ ToFloat()   ← outbound boundary
service (all arithmetic in duit.Money)  ── Stage 3
        │ FromFloat() ← inbound boundary from the legacy model
repository (Scan/Value ↔ NUMERIC(19,4)) ── Stage 2: migrate first
```

## Stage 1 — Add the `duit` dependency

```
go get github.com/bomikun/go-duit
```

```go
import duit "github.com/bomikun/go-duit"
```

The built-in constants already match `NUMERIC(19,4)` and rupiah:

- `ScaleDB = 4` — the persistence scale; must equal the NUMERIC column scale.
- `ScaleIDR = 0` — the scale of user-facing amounts (whole rupiah).

The rounding policy is centralized here: **half-up (ties away from zero)**,
applied only at explicit points (`Round*`, `Div`, `PercentOf`, `Allocate`,
`Value`). `Add/Sub/Mul` are always exact.

## Stage 2 — Repository layer (safest, start here)

The DB columns **do not need to change** — `NUMERIC(19,4)` was always
correct; the bug was reading it into `float64`. Change the field types in
your row/repository structs to `duit.Money` (or `duit.NullMoney` for
nullable columns):

```go
type PaymentRow struct {
    ID     int64
    Amount duit.Money     // previously float64
    Fee    duit.NullMoney // nullable column
}

err := db.QueryRow(`SELECT id, amount, fee FROM payments WHERE id=$1`, id).
    Scan(&row.ID, &row.Amount, &row.Fee)

_, err = db.Exec(`INSERT INTO payments (amount) VALUES ($1)`, row.Amount)
```

No manual conversion: `Money` implements `sql.Scanner` and
`driver.Valuer`. On writes, `Value()` always rounds to `ScaleDB`, so the
representation stored in the DB is canonical.

While the models above are still `float64`, convert **once** at the
repository→model boundary with `row.Amount.ToFloat()`.

## Stage 3 — Service layer: all arithmetic moves to Money

Replace `float64` calculations with `Money` operations. The conversion
pattern:

```go
func BuildInvoice(m InvoiceModel, n int) (InvoiceViewModel, error) {
    price := duit.FromFloat(m.UnitPrice) // inbound boundary — the only FromFloat

    subtotal := price.MulInt(m.Qty)                            // exact
    discount := subtotal.PercentOf(discountRate, duit.ScaleDB) // explicit rounding
    tax      := subtotal.Sub(discount).PercentOf(vatRate, duit.ScaleDB)
    total    := subtotal.Sub(discount).Add(tax).RoundIDR()     // bill: whole rupiah

    per, err := total.DivInt(int64(n), duit.ScaleIDR) // Div REQUIRES a scale
    ...
    return InvoiceViewModel{Total: total.ToFloat(), ...}, nil // outbound boundary
}
```

Rules the API enforces:

- **`Div` has no default scale.** Division is where float64 drift was
  silently born, so every call site must state the precision it wants
  (`ScaleDB` inside calculations, `ScaleIDR` at the user boundary).
- **Split evenly with `Allocate`, not `Div`.** `Allocate(3, ScaleIDR)` on
  100 yields `[34 33 33]`, which sums back to exactly 100 — not a single
  rupiah is lost or invented.
- Never feed a `ToFloat()` result back into monetary arithmetic.

## Stage 4 — Models, viewmodels, and JSON

Once the repository and service layers are clean, change the
model/viewmodel fields to `duit.Money` and delete their
`FromFloat`/`ToFloat` pairs.

JSON compatibility is designed for the transition period:

- **Outbound**: `Money` marshals as a **string** (`"115000"`), so precision
  is safe in clients that parse JSON numbers as float64/double.
  Coordinate with the frontend — this is a contract change (number → string).
- **Inbound**: `UnmarshalJSON` accepts **both strings and bare numbers**
  (`"1.15"` and `1.15` are equally valid and equally exact), so old
  clients that still send numbers keep working during the transition.

## Stage 5 — Burn-down

`FromFloat` and `ToFloat` are marked `Deprecated`, so:

```
staticcheck ./...   # SA1019 flags every remaining float64 boundary usage
```

Resolve them one by one; when the count reaches zero, the migration is done.

## Quick checklist

- [ ] `go get github.com/bomikun/go-duit`, build green
- [ ] Repository: row fields → `Money`/`NullMoney`, delete manual conversions
- [ ] Service: arithmetic → `Money`; `Div`/`PercentOf` with explicit scales; even splits → `Allocate`
- [ ] Audit: no `ToFloat()` result is ever used in money math again
- [ ] Coordinate with the frontend: JSON amounts become strings
- [ ] Models/viewmodels → `Money`, remove `FromFloat`/`ToFloat` call sites
- [ ] `staticcheck` clean of SA1019 for the duit package
