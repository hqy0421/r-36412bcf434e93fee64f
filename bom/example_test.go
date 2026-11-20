package bom_test

import (
	"bytes"
	"fmt"

	"github.com/sclevine/agouti/bom"
)

// Example shows planning a purchasing daily report from an in-memory Graph.
// The same usage line shown twice is counted once; two distinct lines that
// reach the same leaf are both accumulated; inventory is deducted once from
// the aggregate demand.
func Example() {
	graph := bom.NewGraph()

	_ = graph.AddOrder(bom.Order{
		ID:       "SO-1",
		Item:     bom.Key{Version: "v1", MaterialID: "widget"},
		Quantity: 10,
	})
	_ = graph.AddRecipe(bom.Recipe{
		Key: bom.Key{Version: "v1", MaterialID: "widget"},
		Lines: []bom.UsageLine{
			{ID: "L-1", Child: bom.Key{Version: "v1", MaterialID: "frame"}, Quantity: 1},
			{ID: "L-2", Child: bom.Key{Version: "v1", MaterialID: "frame"}, Quantity: 1},
		},
	})
	_ = graph.AddRecipe(bom.Recipe{
		Key: bom.Key{Version: "v1", MaterialID: "frame"},
		Lines: []bom.UsageLine{
			{ID: "F-1", Child: bom.Key{Version: "v1", MaterialID: "screw"}, Quantity: 4},
		},
	})
	_ = graph.AddRecipe(bom.Recipe{Key: bom.Key{Version: "v1", MaterialID: "screw"}, Leaf: true})
	_ = graph.AddStock(bom.Stock{
		Key:           bom.Key{Version: "v1", MaterialID: "screw"},
		Name:          "M3 screw",
		Supplier:      "Fasteners Ltd",
		UnitCostCents: 5,
		OnHand:        30,
	})

	report, err := bom.Run(graph, []string{"SO-1"})
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, requirement := range report.Requirements {
		fmt.Printf("%s: demand %d, on hand %d, net purchase %d\n",
			requirement.Key.MaterialID, requirement.Demand, requirement.OnHand, requirement.NetPurchase)
	}
	fmt.Printf("order gross cost: %d cents\n", report.GrossCostCents)
	fmt.Printf("net purchase cost: %d cents\n", report.NetPurchaseCostCents)

	var details bytes.Buffer
	_ = report.WriteDetails(&details)
	fmt.Println(len(details.String()) > 0)

	// Output:
	// screw: demand 80, on hand 30, net purchase 50
	// order gross cost: 400 cents
	// net purchase cost: 250 cents
	// true
}
