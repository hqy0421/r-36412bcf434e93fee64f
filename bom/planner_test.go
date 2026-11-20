package bom_test

import (
	"bytes"
	"math"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/sclevine/agouti/bom"
)

var _ = Describe("Planner", func() {
	var (
		graph *bom.Graph
	)

	leaf := func(id, version string, cost, onHand int64) bom.Stock {
		return bom.Stock{
			Key:           bom.Key{Version: version, MaterialID: id},
			Name:          "name-" + id,
			Supplier:      "supplier-" + id,
			UnitCostCents: cost,
			OnHand:        onHand,
		}
	}

	addLeaf := func(id, version string, cost, onHand int64) {
		Expect(graph.AddRecipe(bom.Recipe{
			Key:  bom.Key{Version: version, MaterialID: id},
			Leaf: true,
		})).To(Succeed())
		Expect(graph.AddStock(leaf(id, version, cost, onHand))).To(Succeed())
	}

	BeforeEach(func() {
		graph = bom.NewGraph()
	})

	It("accumulates distinct usage lines and paths, counting repeated lines once", func() {
		// Finished product P (v1) uses sub-assembly S twice, via two distinct
		// usage lines. S in turn uses leaf X once through two distinct lines.
		Expect(graph.AddOrder(bom.Order{
			ID:       "O1",
			Item:     bom.Key{Version: "v1", MaterialID: "P"},
			Quantity: 3,
		})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:  bom.Key{Version: "v1", MaterialID: "P"},
			Leaf: false,
			Lines: []bom.UsageLine{
				{ID: "P-1", Child: bom.Key{Version: "v1", MaterialID: "S"}, Quantity: 2},
				{ID: "P-2", Child: bom.Key{Version: "v1", MaterialID: "S"}, Quantity: 1},
				// The page re-rendered the same line twice; this must not
				// double count.
				{ID: "P-1", Child: bom.Key{Version: "v1", MaterialID: "S"}, Quantity: 2},
			},
		})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:  bom.Key{Version: "v1", MaterialID: "S"},
			Leaf: false,
			Lines: []bom.UsageLine{
				{ID: "S-1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 4},
				{ID: "S-2", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 1},
			},
		})).To(Succeed())
		addLeaf("X", "v1", 250, 0)

		report, err := bom.Run(graph, []string{"O1"})
		Expect(err).NotTo(HaveOccurred())

		// Per unit of P: S appears via two lines (2 + 1); each S needs 4 + 1
		// of X. So per P: (2+1) * (4+1) = 15 X; times 3 units = 45.
		Expect(report.Requirements).To(HaveLen(1))
		Expect(report.Requirements[0].Key).To(Equal(bom.Key{Version: "v1", MaterialID: "X"}))
		Expect(report.Requirements[0].Demand).To(Equal(int64(45)))
		Expect(report.Requirements[0].NetPurchase).To(Equal(int64(45)))
		Expect(report.NetPurchaseCostCents).To(Equal(int64(45 * 250)))

		// Usage rows are deduplicated by (parent, line ID).
		Expect(report.Usage).To(HaveLen(4))
	})

	It("deducts inventory once against the aggregate demand", func() {
		Expect(graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: 2})).To(Succeed())
		Expect(graph.AddOrder(bom.Order{ID: "O2", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: 4})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:  bom.Key{Version: "v1", MaterialID: "P"},
			Leaf: false,
			Lines: []bom.UsageLine{
				{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 10},
			},
		})).To(Succeed())
		addLeaf("X", "v1", 100, 15)

		report, err := bom.Run(graph, []string{"O1", "O2"})
		Expect(err).NotTo(HaveOccurred())

		// Demand is (2 + 4) * 10 = 60. Stock is deducted once: 60 - 15 = 45.
		Expect(report.Orders).To(HaveLen(2))
		Expect(report.Orders[0].Leaves[0].Quantity).To(Equal(int64(20)))
		Expect(report.Orders[1].Leaves[0].Quantity).To(Equal(int64(40)))
		Expect(report.GrossCostCents).To(Equal(int64(60 * 100)))
		Expect(report.Requirements[0].OnHand).To(Equal(int64(15)))
		Expect(report.Requirements[0].NetPurchase).To(Equal(int64(45)))
		Expect(report.NetPurchaseCostCents).To(Equal(int64(45 * 100)))
	})

	It("keeps zero-purchase rows and their source versions", func() {
		Expect(graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: 1})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:  bom.Key{Version: "v1", MaterialID: "P"},
			Leaf: false,
			Lines: []bom.UsageLine{
				{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 5},
			},
		})).To(Succeed())
		addLeaf("X", "v1", 100, 50)

		report, err := bom.Run(graph, []string{"O1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Requirements).To(HaveLen(1))
		Expect(report.Requirements[0].Key).To(Equal(bom.Key{Version: "v1", MaterialID: "X"}))
		Expect(report.Requirements[0].NetPurchase).To(Equal(int64(0)))
		Expect(report.Requirements[0].CostCents).To(Equal(int64(0)))
		Expect(report.Suppliers).To(HaveLen(1))
		Expect(report.Suppliers[0].Lines).To(HaveLen(1))
		Expect(report.Suppliers[0].Lines[0].NetPurchase).To(Equal(int64(0)))
	})

	It("fails on a cycle", func() {
		Expect(graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "A"}, Quantity: 1})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "A"},
			Lines: []bom.UsageLine{{ID: "A-1", Child: bom.Key{Version: "v1", MaterialID: "B"}, Quantity: 1}},
		})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "B"},
			Lines: []bom.UsageLine{{ID: "B-1", Child: bom.Key{Version: "v1", MaterialID: "A"}, Quantity: 1}},
		})).To(Succeed())

		_, err := bom.Run(graph, []string{"O1"})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrCycle))
	})

	It("fails when a reachable recipe is missing", func() {
		Expect(graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: 1})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 1}},
		})).To(Succeed())

		_, err := bom.Run(graph, []string{"O1"})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrMissing))
	})

	It("fails when a leaf has no stock record", func() {
		Expect(graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: 1})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 1}},
		})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{Key: bom.Key{Version: "v1", MaterialID: "X"}, Leaf: true})).To(Succeed())

		_, err := bom.Run(graph, []string{"O1"})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrMissing))
	})

	It("fails when one usage-line ID carries conflicting payloads", func() {
		Expect(graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: 1})).To(Succeed())
		err := graph.AddRecipe(bom.Recipe{
			Key: bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{
				{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 1},
				{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 2},
			},
		})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrIdentityConflict))
	})

	It("fails when a material is both leaf and non-leaf", func() {
		Expect(graph.AddRecipe(bom.Recipe{Key: bom.Key{Version: "v1", MaterialID: "X"}, Leaf: true})).To(Succeed())
		err := graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "X"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "Y"}, Quantity: 1}},
		})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrIdentityConflict))
	})

	It("fails when a non-leaf expansion is empty", func() {
		err := graph.AddRecipe(bom.Recipe{Key: bom.Key{Version: "v1", MaterialID: "P"}})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrEmptyExpansion))
	})

	It("fails on quantity overflow", func() {
		Expect(graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: math.MaxInt64})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 2}},
		})).To(Succeed())
		addLeaf("X", "v1", 1, 0)

		_, err := bom.Run(graph, []string{"O1"})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrOverflow))
	})

	It("fails on cost overflow", func() {
		Expect(graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: math.MaxInt64/2 + 1})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 1}},
		})).To(Succeed())
		addLeaf("X", "v1", 2, 0)

		_, err := bom.Run(graph, []string{"O1"})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrOverflow))
	})

	It("fails when the same order ID resolves to conflicting records", func() {
		Expect(graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: 1})).To(Succeed())
		err := graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: 2})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrIdentityConflict))
	})

	It("writes usage, order-leaf and requirement detail rows", func() {
		Expect(graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: 1})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 2}},
		})).To(Succeed())
		addLeaf("X", "v1", 150, 0)

		report, err := bom.Run(graph, []string{"O1"})
		Expect(err).NotTo(HaveOccurred())

		var details bytes.Buffer
		Expect(report.WriteDetails(&details)).To(Succeed())
		lines := strings.Split(strings.TrimSpace(details.String()), "\n")
		Expect(lines).To(HaveLen(6))
		Expect(lines[0]).To(ContainSubstring("usage"))
		Expect(lines[1]).To(ContainSubstring("L1"))
		Expect(lines[2]).To(ContainSubstring("order_id"))
		Expect(lines[3]).To(ContainSubstring("order_leaf,O1,v1,P"))
		Expect(lines[4]).To(ContainSubstring("leaf_version"))
		Expect(lines[5]).To(ContainSubstring("requirement,v1,X"))

		var human bytes.Buffer
		Expect(report.WriteDailyReport(&human)).To(Succeed())
		Expect(human.String()).To(ContainSubstring("Purchasing daily report"))
		Expect(human.String()).To(ContainSubstring("1.50"))
	})

	It("plans each order once when an order ID is repeated", func() {
		Expect(graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: 1})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 1}},
		})).To(Succeed())
		addLeaf("X", "v1", 100, 0)

		report, err := bom.Run(graph, []string{"O1", "O1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(report.Orders).To(HaveLen(1))
		Expect(report.Requirements[0].Demand).To(Equal(int64(1)))
	})

	It("fails for an unknown order", func() {
		_, err := bom.Run(graph, []string{"nope"})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrMissing))
	})
})
