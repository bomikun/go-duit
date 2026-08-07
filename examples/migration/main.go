// Example of gradually migrating money handling from float64 to duit.Money.
//
// Scenario: an existing codebase has float64 models and viewmodels that
// must NOT be changed yet. The middle layers migrate first (repository
// scan/value and service calculations). float64 survives only at the
// boundaries, through duit.FromFloat / duit.ToFloat — both deliberately
// marked Deprecated so every remaining call site shows up in
// staticcheck/linters.
//
//	handler (float64 viewmodel)
//	   │  ToFloat()  ← outbound boundary
//	service (all arithmetic in duit.Money)
//	   │  FromFloat() ← inbound boundary from the legacy model
//	repository (Scan/Value directly Money ↔ NUMERIC(19,4))
//
// Run it with: go run ./examples/migration
package main

import (
	"encoding/json"
	"fmt"

	duit "github.com/bomikun/go-duit"
)

// ---------------------------------------------------------------------------
// STAGE 0 — legacy code (do not change yet)
// ---------------------------------------------------------------------------

// InvoiceModel mimics an existing model that still uses float64.
type InvoiceModel struct {
	ID        int64
	UnitPrice float64
	Qty       int64
}

// InvoiceViewModel mimics an existing viewmodel that still uses float64.
type InvoiceViewModel struct {
	Subtotal    float64 `json:"subtotal"`
	Discount    float64 `json:"discount"`
	Tax         float64 `json:"tax"`
	Total       float64 `json:"total"`
	Installment float64 `json:"installment"`
}

// ---------------------------------------------------------------------------
// STAGE 1 — repository: NUMERIC(19,4) columns scan directly into Money
// ---------------------------------------------------------------------------

// PaymentRow is a new repository-layer struct that already uses Money.
// Thanks to sql.Scanner/driver.Valuer it is used exactly like float64 was:
//
//	err := db.QueryRow(
//	    `SELECT id, amount, fee FROM payments WHERE id = $1`, id,
//	).Scan(&row.ID, &row.Amount, &row.Fee)
//
//	_, err = db.Exec(
//	    `INSERT INTO payments (id, amount, fee) VALUES ($1, $2, $3)`,
//	    row.ID, row.Amount, row.Fee, // Money goes straight in as a parameter
//	)
type PaymentRow struct {
	ID     int64
	Amount duit.Money
	Fee    duit.NullMoney // nullable column → NullMoney
}

// fakeDBRoundTrip simulates what a Postgres driver does: Value() on write,
// Scan() on read. In a real repository this is replaced by actual
// db.Exec/QueryRow calls.
func fakeDBRoundTrip(row PaymentRow) PaymentRow {
	amountVal, _ := row.Amount.Value() // "1665000.0000" — canonical NUMERIC(19,4)
	feeVal, _ := row.Fee.Value()
	fmt.Printf("  write to DB : amount=%v fee=%v\n", amountVal, feeVal)

	var out PaymentRow
	out.ID = row.ID
	_ = out.Amount.Scan([]byte(amountVal.(string))) // pq/pgx deliver []byte
	_ = out.Fee.Scan(nil)                           // NULL column
	return out
}

// ---------------------------------------------------------------------------
// STAGE 2 — service: all arithmetic moves to Money
// ---------------------------------------------------------------------------

var (
	vatRate      = duit.NewFromInt(11)        // VAT (PPN) 11%
	discountRate = duit.MustFromString("2.5") // 2.5% discount
)

// BuildInvoice takes the legacy model (float64) and returns the legacy
// viewmodel (float64), but every calculation in between uses Money.
func BuildInvoice(m InvoiceModel, installments int) (InvoiceViewModel, error) {
	// Inbound boundary: the only float64 → Money conversion.
	unitPrice := duit.FromFloat(m.UnitPrice) //nolint:staticcheck // legacy model boundary

	subtotal := unitPrice.MulInt(m.Qty)                        // exact
	discount := subtotal.PercentOf(discountRate, duit.ScaleDB) // centralized rounding
	taxBase := subtotal.Sub(discount)
	tax := taxBase.PercentOf(vatRate, duit.ScaleDB)
	total := taxBase.Add(tax).RoundIDR() // user-facing bill: whole rupiah

	// Installments: Div demands an explicit scale — whole rupiah here.
	perInstallment, err := total.DivInt(int64(installments), duit.ScaleIDR)
	if err != nil {
		return InvoiceViewModel{}, err
	}

	// Outbound boundary: the only Money → float64 conversion.
	//nolint:staticcheck // legacy viewmodel boundary, delete once it migrates
	return InvoiceViewModel{
		Subtotal:    subtotal.ToFloat(),
		Discount:    discount.ToFloat(),
		Tax:         tax.ToFloat(),
		Total:       total.ToFloat(),
		Installment: perInstallment.ToFloat(),
	}, nil
}

// ---------------------------------------------------------------------------
// STAGE 3 — new viewmodel: Money appears as a string in JSON
// ---------------------------------------------------------------------------

// InvoiceResponseV2 is the final shape once the viewmodel migrates too:
// its fields are duit.Money and serialize as JSON strings automatically,
// so precision survives all the way to the client.
type InvoiceResponseV2 struct {
	Total        duit.Money   `json:"total"`
	Installments []duit.Money `json:"installments"`
}

func main() {
	// The classic case: 1.15 * 100.
	fmt.Println("== why migrate ==")
	f := 1.15
	fmt.Printf("  float64: 1.15 * 100 = %v\n", f*100)
	fmt.Printf("  Money  : 1.15 * 100 = %v\n\n", duit.MustFromString("1.15").MulInt(100))

	// Legacy float64 model in, Money for the math, legacy viewmodel out.
	fmt.Println("== service with legacy float64 model/viewmodel ==")
	model := InvoiceModel{ID: 1, UnitPrice: 512_500.50, Qty: 3}
	vm, err := BuildInvoice(model, 3)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  subtotal=%.4f discount=%.4f vat=%.4f total=%.0f installment=%.0f\n\n",
		vm.Subtotal, vm.Discount, vm.Tax, vm.Total, vm.Installment)

	// Repository round-trip: Money ↔ NUMERIC(19,4), no manual conversion.
	fmt.Println("== repository: simulated NUMERIC(19,4) round-trip ==")
	row := PaymentRow{ID: 1, Amount: duit.FromFloat(vm.Total)} //nolint:staticcheck
	back := fakeDBRoundTrip(row)
	fmt.Printf("  read from DB: amount=%v fee.Valid=%v\n\n", back.Amount, back.Fee.Valid)

	// Split a bill without losing a single rupiah.
	fmt.Println("== allocate: split the bill in 3 with no drift ==")
	total := duit.FromFloat(vm.Total) //nolint:staticcheck
	parts, err := total.Allocate(3, duit.ScaleIDR)
	if err != nil {
		panic(err)
	}
	fmt.Printf("  %v into %v (sums back to %v)\n\n",
		total, parts, duit.Sum(parts[0], parts[1:]...))

	// The final shape: JSON with amounts as strings.
	fmt.Println("== new viewmodel: JSON strings ==")
	out, _ := json.MarshalIndent(InvoiceResponseV2{Total: total, Installments: parts}, "  ", "  ")
	fmt.Println("  " + string(out))
}
