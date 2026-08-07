package duit_test

import (
	"encoding/json"
	"fmt"

	duit "github.com/bomikun/go-duit"
)

// The classic float64 trap: 1.15 * 100. Money keeps it exact.
func ExampleMoney_MulInt() {
	f := 1.15
	fmt.Println(f * 100)
	fmt.Println(duit.MustFromString("1.15").MulInt(100))
	// Output:
	// 114.99999999999999
	// 115
}

// Division always states its scale, so precision loss is a visible,
// reviewed decision instead of an accident.
func ExampleMoney_Div() {
	one := duit.NewFromInt(1)

	third, _ := one.DivInt(3, duit.ScaleDB)
	fmt.Println(third)

	perItem, _ := duit.NewFromInt(250_000).DivInt(3, duit.ScaleIDR)
	fmt.Println(perItem)
	// Output:
	// 0.3333
	// 83333
}

func ExampleMoney_PercentOf() {
	price := duit.NewFromInt(1_500_000)
	ppn := price.PercentOf(duit.NewFromInt(11), duit.ScaleIDR)
	fmt.Println(ppn)
	fmt.Println(price.Add(ppn))
	// Output:
	// 165000
	// 1665000
}

// Allocate never loses a unit: the parts always sum back to the total.
func ExampleMoney_Allocate() {
	parts, _ := duit.NewFromInt(100).Allocate(3, duit.ScaleIDR)
	fmt.Println(parts)

	parts, _ = duit.NewFromInt(100).Allocate(3, duit.ScaleDB)
	fmt.Println(parts)
	// Output:
	// [34 33 33]
	// [33.3334 33.3333 33.3333]
}

// Money marshals as a JSON string and still accepts numbers from
// clients that have not migrated yet.
func ExampleMoney_MarshalJSON() {
	type Invoice struct {
		Total duit.Money `json:"total"`
	}

	out, _ := json.Marshal(Invoice{Total: duit.MustFromString("1.15").MulInt(100)})
	fmt.Println(string(out))

	var in Invoice
	_ = json.Unmarshal([]byte(`{"total":1.15}`), &in)
	fmt.Println(in.Total)
	// Output:
	// {"total":"115"}
	// 1.15
}
