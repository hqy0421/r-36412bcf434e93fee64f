package bom_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/sclevine/agouti/bom"
)

var _ = Describe("Graph", func() {
	var graph *bom.Graph

	BeforeEach(func() {
		graph = bom.NewGraph()
	})

	It("merges repeated identical recipes without duplicating lines", func() {
		recipe := bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 2}},
		}
		Expect(graph.AddRecipe(recipe)).To(Succeed())
		Expect(graph.AddRecipe(recipe)).To(Succeed())

		stored, err := graph.Recipe(recipe.Key)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Lines).To(HaveLen(1))
	})

	It("merges disjoint line sets from repeated renders", func() {
		Expect(graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 2}},
		})).To(Succeed())
		Expect(graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L2", Child: bom.Key{Version: "v1", MaterialID: "Y"}, Quantity: 3}},
		})).To(Succeed())

		stored, err := graph.Recipe(bom.Key{Version: "v1", MaterialID: "P"})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Lines).To(HaveLen(2))
	})

	It("rejects a line ID that reappears with a different payload", func() {
		Expect(graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 2}},
		})).To(Succeed())

		err := graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: 7}},
		})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrIdentityConflict))
	})

	It("rejects conflicting stock records", func() {
		key := bom.Key{Version: "v1", MaterialID: "X"}
		Expect(graph.AddStock(bom.Stock{Key: key, Name: "x", Supplier: "s", OnHand: 1})).To(Succeed())

		err := graph.AddStock(bom.Stock{Key: key, Name: "x", Supplier: "s", OnHand: 2})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrIdentityConflict))
	})

	It("keeps different completion versions apart", func() {
		Expect(graph.AddStock(bom.Stock{
			Key:      bom.Key{Version: "v1", MaterialID: "X"},
			Name:     "x",
			Supplier: "s",
		})).To(Succeed())
		Expect(graph.AddStock(bom.Stock{
			Key:      bom.Key{Version: "v2", MaterialID: "X"},
			Name:     "x",
			Supplier: "s",
		})).To(Succeed())

		Expect(graph.Stocks()).To(HaveLen(2))
	})

	It("rejects empty or negative quantities", func() {
		err := graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "v1", MaterialID: "P"}, Quantity: -1})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrInvalidQuantity))

		err = graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "v1", MaterialID: "X"}, Quantity: -2}},
		})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrInvalidQuantity))
	})

	It("requires a completion version and material ID", func() {
		err := graph.AddOrder(bom.Order{ID: "O1", Item: bom.Key{Version: "", MaterialID: "P"}, Quantity: 1})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrMissing))

		err = graph.AddRecipe(bom.Recipe{
			Key:   bom.Key{Version: "v1", MaterialID: "P"},
			Lines: []bom.UsageLine{{ID: "L1", Child: bom.Key{Version: "", MaterialID: "X"}, Quantity: 1}},
		})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrMissing))
	})
})

var _ = Describe("Kind", func() {
	It("returns nil for foreign errors", func() {
		Expect(bom.Kind(nil)).To(BeNil())
		Expect(bom.Kind(errForeign{})).To(BeNil())
	})
})

type errForeign struct{}

func (errForeign) Error() string { return "foreign" }
