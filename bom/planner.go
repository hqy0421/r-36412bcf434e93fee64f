package bom

import (
	"math"
	"sort"
)

// Planner turns order IDs into a purchasing report by walking a read-only
// Source. A Planner caches nothing between calls; Plan may be called
// repeatedly and is safe to reuse, but a single Planner must not be used
// concurrently.
type Planner struct {
	source Source
}

// NewPlanner returns a Planner backed by source.
func NewPlanner(source Source) *Planner {
	return &Planner{source: source}
}

// Run is shorthand for NewPlanner(source).Plan(orderIDs).
func Run(source Source, orderIDs []string) (*Report, error) {
	return NewPlanner(source).Plan(orderIDs)
}

// Plan expands every order in orderIDs and produces a purchasing report.
//
// Orders are processed in the order given; a repeated order ID is planned
// once. Leaf quantities are attributed to each order before the aggregate
// demand is reduced by on-hand stock exactly once per leaf material.
func (p *Planner) Plan(orderIDs []string) (*Report, error) {
	if p == nil || p.source == nil {
		return nil, wrap(ErrMissing, "no BOM source")
	}

	run := &planRun{
		source:    p.source,
		recipes:   map[Key]Recipe{},
		stocks:    map[Key]Stock{},
		vectors:   map[Key]map[Key]int64{},
		state:     map[Key]int{},
		usageSeen: map[string]bool{},
	}

	report := &Report{}
	seenOrders := map[string]bool{}
	demand := map[Key]int64{}

	for _, orderID := range orderIDs {
		if orderID == "" {
			return nil, wrap(ErrMissing, "order ID is empty")
		}
		if seenOrders[orderID] {
			continue
		}
		seenOrders[orderID] = true

		order, err := run.loadOrder(orderID)
		if err != nil {
			return nil, err
		}

		vector, err := run.vector(order.Item)
		if err != nil {
			return nil, err
		}

		orderReport := OrderReport{Order: order}
		leafKeys := sortedLeafKeys(vector)
		for _, leaf := range leafKeys {
			perUnit := vector[leaf]
			quantity, ok := mulInt64(perUnit, order.Quantity)
			if !ok {
				return nil, wrap(ErrOverflow, "leaf demand for %s in order %q overflows int64", leaf, order.ID)
			}

			// Every reached leaf is reported, even when its quantity works
			// out to zero. Its stock record must still exist so that source
			// versions and IDs are preserved and a zero-purchase row can be
			// kept for it.
			stock, err := run.stock(leaf)
			if err != nil {
				return nil, err
			}
			cost, ok := mulInt64(quantity, stock.UnitCostCents)
			if !ok {
				return nil, wrap(ErrOverflow, "gross cost for leaf %s in order %q overflows int64", leaf, order.ID)
			}
			orderReport.Leaves = append(orderReport.Leaves, OrderLeaf{
				OrderID:       order.ID,
				Leaf:          leaf,
				Name:          stock.Name,
				Supplier:      stock.Supplier,
				Quantity:      quantity,
				UnitCostCents: stock.UnitCostCents,
				CostCents:     cost,
			})

			total, ok := addInt64(orderReport.GrossCostCents, cost)
			if !ok {
				return nil, wrap(ErrOverflow, "gross cost for order %q overflows int64", order.ID)
			}
			orderReport.GrossCostCents = total

			aggregate, ok := addInt64(demand[leaf], quantity)
			if !ok {
				return nil, wrap(ErrOverflow, "aggregate demand for leaf %s overflows int64", leaf)
			}
			demand[leaf] = aggregate
		}

		total, ok := addInt64(report.GrossCostCents, orderReport.GrossCostCents)
		if !ok {
			return nil, wrap(ErrOverflow, "total gross cost overflows int64")
		}
		report.GrossCostCents = total
		report.Orders = append(report.Orders, orderReport)
	}

	if err := run.buildRequirements(report, demand); err != nil {
		return nil, err
	}
	report.Usage = run.usageRows()
	if err := run.buildSuppliers(report); err != nil {
		return nil, err
	}
	return report, nil
}

type planRun struct {
	source    Source
	recipes   map[Key]Recipe
	stocks    map[Key]Stock
	vectors   map[Key]map[Key]int64
	state     map[Key]int
	stack     []Key
	usage     []UsageRow
	usageSeen map[string]bool
}

func (r *planRun) loadOrder(orderID string) (Order, error) {
	order, err := r.source.Order(orderID)
	if err != nil {
		return Order{}, err
	}
	if order.ID != orderID {
		return Order{}, wrap(ErrIdentityConflict, "order %q resolved to %q", orderID, order.ID)
	}
	if !order.Item.valid() {
		return Order{}, wrap(ErrMissing, "order %q has no completion version or material ID", orderID)
	}
	if order.Quantity < 0 {
		return Order{}, wrap(ErrInvalidQuantity, "order %q has negative quantity %d", orderID, order.Quantity)
	}
	return order, nil
}

func (r *planRun) loadRecipe(key Key) (Recipe, error) {
	if recipe, ok := r.recipes[key]; ok {
		return recipe, nil
	}
	if !key.valid() {
		return Recipe{}, wrap(ErrMissing, "material has no completion version or material ID")
	}

	recipe, err := r.source.Recipe(key)
	if err != nil {
		return Recipe{}, err
	}
	if recipe.Key != key {
		return Recipe{}, wrap(ErrIdentityConflict, "recipe for %s resolved to %s", key, recipe.Key)
	}
	if recipe.Leaf && len(recipe.Lines) > 0 {
		return Recipe{}, wrap(ErrIdentityConflict, "leaf material %s also has %d usage lines", key, len(recipe.Lines))
	}
	if !recipe.Leaf && len(recipe.Lines) == 0 {
		return Recipe{}, wrap(ErrEmptyExpansion, "non-leaf material %s has no usage lines", key)
	}

	lines, err := normalizeLines(recipe.Lines)
	if err != nil {
		return Recipe{}, err
	}
	recipe.Lines = lines
	r.recipes[key] = recipe
	return recipe, nil
}

func (r *planRun) stock(key Key) (Stock, error) {
	if stock, ok := r.stocks[key]; ok {
		return stock, nil
	}
	if !key.valid() {
		return Stock{}, wrap(ErrMissing, "leaf material has no completion version or material ID")
	}
	stock, err := r.source.Stock(key)
	if err != nil {
		return Stock{}, err
	}
	if stock.Key != key {
		return Stock{}, wrap(ErrIdentityConflict, "stock record for %s resolved to %s", key, stock.Key)
	}
	if err := validateStock(stock); err != nil {
		return Stock{}, err
	}
	r.stocks[key] = stock
	return stock, nil
}

// vector returns the leaf quantities required for one unit of key.
func (r *planRun) vector(key Key) (map[Key]int64, error) {
	switch r.state[key] {
	case 1:
		return nil, wrap(ErrCycle, "%s", r.cyclePath(key))
	case 2:
		return r.vectors[key], nil
	}

	recipe, err := r.loadRecipe(key)
	if err != nil {
		return nil, err
	}

	r.state[key] = 1
	r.stack = append(r.stack, key)

	var vector map[Key]int64
	if recipe.Leaf {
		if _, err := r.stock(key); err != nil {
			r.pop(key)
			return nil, err
		}
		vector = map[Key]int64{key: 1}
	} else {
		vector = map[Key]int64{}
		for _, line := range recipe.Lines {
			if !line.Child.valid() {
				r.pop(key)
				return nil, wrap(ErrMissing, "usage line %q in %s has no child completion version or material ID", line.ID, key)
			}
			if line.Quantity < 0 {
				r.pop(key)
				return nil, wrap(ErrInvalidQuantity, "usage line %q in %s has negative quantity %d", line.ID, key, line.Quantity)
			}

			childRecipe, err := r.loadRecipe(line.Child)
			if err != nil {
				r.pop(key)
				return nil, err
			}
			childVector, err := r.vector(line.Child)
			if err != nil {
				r.pop(key)
				return nil, err
			}

			r.recordUsage(key, line, childRecipe.Leaf)

			for leaf, perUnit := range childVector {
				scaled, ok := mulInt64(perUnit, line.Quantity)
				if !ok {
					r.pop(key)
					return nil, wrap(ErrOverflow, "requirement for leaf %s via line %q in %s overflows int64", leaf, line.ID, key)
				}
				total, ok := addInt64(vector[leaf], scaled)
				if !ok {
					r.pop(key)
					return nil, wrap(ErrOverflow, "requirement for leaf %s in %s overflows int64", leaf, key)
				}
				vector[leaf] = total
			}
		}
	}

	r.pop(key)
	r.state[key] = 2
	r.vectors[key] = vector
	return vector, nil
}

func (r *planRun) pop(key Key) {
	if len(r.stack) > 0 && r.stack[len(r.stack)-1] == key {
		r.stack = r.stack[:len(r.stack)-1]
		return
	}
	for i := len(r.stack) - 1; i >= 0; i-- {
		if r.stack[i] == key {
			r.stack = append(r.stack[:i], r.stack[i+1:]...)
			return
		}
	}
}

func (r *planRun) cyclePath(key Key) string {
	path := ""
	for i, entry := range r.stack {
		if entry == key {
			path = formatCycle(r.stack[i:], key)
			break
		}
	}
	if path == "" {
		path = key.String()
	}
	return path
}

func formatCycle(stack []Key, key Key) string {
	path := ""
	for _, entry := range stack {
		path += entry.String() + " -> "
	}
	return path + key.String()
}

func (r *planRun) recordUsage(parent Key, line UsageLine, childLeaf bool) {
	key := parent.String() + "\x00" + line.ID
	if r.usageSeen[key] {
		return
	}
	r.usageSeen[key] = true
	r.usage = append(r.usage, UsageRow{
		Parent:    parent,
		LineID:    line.ID,
		Child:     line.Child,
		Quantity:  line.Quantity,
		ChildLeaf: childLeaf,
	})
}

func (r *planRun) usageRows() []UsageRow {
	rows := append([]UsageRow(nil), r.usage...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Parent != rows[j].Parent {
			return lessKey(rows[i].Parent, rows[j].Parent)
		}
		return rows[i].LineID < rows[j].LineID
	})
	return rows
}

func (r *planRun) buildRequirements(report *Report, demand map[Key]int64) error {
	keys := make([]Key, 0, len(demand))
	for key := range demand {
		keys = append(keys, key)
	}
	sortKeys(keys)

	for _, key := range keys {
		stock, err := r.stock(key)
		if err != nil {
			return err
		}
		quantity := demand[key]
		net := int64(0)
		if quantity > stock.OnHand {
			net = quantity - stock.OnHand
		}
		cost, ok := mulInt64(net, stock.UnitCostCents)
		if !ok {
			return wrap(ErrOverflow, "net purchase cost for leaf %s overflows int64", key)
		}
		report.Requirements = append(report.Requirements, Requirement{
			Key:           key,
			Name:          stock.Name,
			Supplier:      stock.Supplier,
			UnitCostCents: stock.UnitCostCents,
			Demand:        quantity,
			OnHand:        stock.OnHand,
			NetPurchase:   net,
			CostCents:     cost,
		})
		total, ok := addInt64(report.NetPurchaseCostCents, cost)
		if !ok {
			return wrap(ErrOverflow, "total net purchase cost overflows int64")
		}
		report.NetPurchaseCostCents = total
	}
	return nil
}

func (r *planRun) buildSuppliers(report *Report) error {
	bySupplier := map[string]*SupplierReport{}
	for _, requirement := range report.Requirements {
		supplier, ok := bySupplier[requirement.Supplier]
		if !ok {
			supplier = &SupplierReport{Supplier: requirement.Supplier}
			bySupplier[requirement.Supplier] = supplier
		}
		supplier.Lines = append(supplier.Lines, SupplierLine{
			Key:           requirement.Key,
			Name:          requirement.Name,
			NetPurchase:   requirement.NetPurchase,
			UnitCostCents: requirement.UnitCostCents,
			CostCents:     requirement.CostCents,
		})
	}

	names := make([]string, 0, len(bySupplier))
	for name := range bySupplier {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		supplier := bySupplier[name]
		sort.Slice(supplier.Lines, func(i, j int) bool {
			return lessKey(supplier.Lines[i].Key, supplier.Lines[j].Key)
		})
		for _, line := range supplier.Lines {
			total, ok := addInt64(supplier.NetPurchase, line.NetPurchase)
			if !ok {
				return wrap(ErrOverflow, "supplier %q net purchase overflows int64", name)
			}
			supplier.NetPurchase = total

			total, ok = addInt64(supplier.CostCents, line.CostCents)
			if !ok {
				return wrap(ErrOverflow, "supplier %q cost overflows int64", name)
			}
			supplier.CostCents = total
		}
		report.Suppliers = append(report.Suppliers, *supplier)
	}
	return nil
}

func sortedLeafKeys(vector map[Key]int64) []Key {
	keys := make([]Key, 0, len(vector))
	for key := range vector {
		keys = append(keys, key)
	}
	sortKeys(keys)
	return keys
}

func lessKey(a, b Key) bool {
	if a.Version != b.Version {
		return a.Version < b.Version
	}
	return a.MaterialID < b.MaterialID
}

func mulInt64(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if a == 0 || b == 0 {
		return 0, true
	}
	if a > math.MaxInt64/b {
		return 0, false
	}
	return a * b, true
}

func addInt64(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if a > math.MaxInt64-b {
		return 0, false
	}
	return a + b, true
}
