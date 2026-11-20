package bom

import "sort"

// Key identifies a BOM material by its completion version and material ID.
// Two records with the same material ID but different completion versions are
// distinct materials.
type Key struct {
	Version    string
	MaterialID string
}

func (k Key) valid() bool {
	return k.Version != "" && k.MaterialID != ""
}

func (k Key) String() string {
	return k.Version + "/" + k.MaterialID
}

// Order is a production order: how many units of a finished material must be
// produced.
type Order struct {
	ID       string
	Item     Key
	Quantity int64
}

// UsageLine is one line item inside a recipe. ID identifies the line; Parent
// is implied by the containing recipe. Quantity is a non-negative integer.
type UsageLine struct {
	ID       string
	Child    Key
	Quantity int64
}

// Recipe is the complete set of direct usage lines for one material. A leaf
// recipe has no lines and represents a purchasable material.
type Recipe struct {
	Key   Key
	Leaf  bool
	Lines []UsageLine
}

// Stock is the leaf-material record: display name, supplier, unit cost in
// cents and the on-hand quantity. Reading it never changes it.
type Stock struct {
	Key           Key
	Name          string
	Supplier      string
	UnitCostCents int64
	OnHand        int64
}

// Source provides BOM records. Implementations must be read-only: no method
// may change remote inventory.
type Source interface {
	Order(orderID string) (Order, error)
	Recipe(key Key) (Recipe, error)
	Stock(key Key) (Stock, error)
}

// Graph is an in-memory Source. It is the value produced by PageSource and is
// also convenient for tests and for planning from imported data.
type Graph struct {
	orders   map[string]Order
	recipes  map[Key]Recipe
	stocks   map[Key]Stock
	revision int
}

// NewGraph returns an empty Graph.
func NewGraph() *Graph {
	return &Graph{
		orders:  map[string]Order{},
		recipes: map[Key]Recipe{},
		stocks:  map[Key]Stock{},
	}
}

// AddOrder records an order. Adding the same order ID twice with different
// contents is an identity conflict; an identical repeat is ignored.
func (g *Graph) AddOrder(order Order) error {
	if order.ID == "" {
		return wrap(ErrMissing, "order ID is empty")
	}
	if !order.Item.valid() {
		return wrap(ErrMissing, "order %q has no completion version or material ID", order.ID)
	}
	if order.Quantity < 0 {
		return wrap(ErrInvalidQuantity, "order %q has negative quantity %d", order.ID, order.Quantity)
	}
	if existing, ok := g.orders[order.ID]; ok {
		if existing != order {
			return wrap(ErrIdentityConflict, "order %q has conflicting records", order.ID)
		}
		return nil
	}
	g.orders[order.ID] = order
	g.revision++
	return nil
}

// AddRecipe records a recipe. Repeats of the same recipe are merged line by
// line: the same usage line seen again is kept once, while a usage line with
// the same ID but a different payload is an identity conflict.
func (g *Graph) AddRecipe(recipe Recipe) error {
	if !recipe.Key.valid() {
		return wrap(ErrMissing, "recipe has no completion version or material ID")
	}
	lines, err := normalizeLines(recipe.Lines)
	if err != nil {
		return err
	}
	if recipe.Leaf && len(lines) > 0 {
		return wrap(ErrIdentityConflict, "leaf material %s also has %d usage lines", recipe.Key, len(lines))
	}
	if !recipe.Leaf && len(lines) == 0 {
		return wrap(ErrEmptyExpansion, "non-leaf material %s has no usage lines", recipe.Key)
	}

	if existing, ok := g.recipes[recipe.Key]; ok {
		if existing.Leaf != recipe.Leaf {
			return wrap(ErrIdentityConflict, "material %s is both leaf and non-leaf", recipe.Key)
		}
		merged, err := mergeLines(existing.Lines, lines)
		if err != nil {
			return wrap(ErrIdentityConflict, "material %s: %s", recipe.Key, err)
		}
		if sameLines(existing.Lines, merged) {
			return nil
		}
		existing.Lines = merged
		g.recipes[recipe.Key] = existing
		g.revision++
		return nil
	}
	g.recipes[recipe.Key] = Recipe{Key: recipe.Key, Leaf: recipe.Leaf, Lines: lines}
	g.revision++
	return nil
}

// AddStock records a leaf stock record. The same key with different contents
// is an identity conflict; an identical repeat is ignored.
func (g *Graph) AddStock(stock Stock) error {
	if !stock.Key.valid() {
		return wrap(ErrMissing, "stock record has no completion version or material ID")
	}
	if err := validateStock(stock); err != nil {
		return err
	}
	if existing, ok := g.stocks[stock.Key]; ok {
		if existing != stock {
			return wrap(ErrIdentityConflict, "material %s has conflicting stock records", stock.Key)
		}
		return nil
	}
	g.stocks[stock.Key] = stock
	g.revision++
	return nil
}

// Order implements Source.
func (g *Graph) Order(orderID string) (Order, error) {
	order, ok := g.orders[orderID]
	if !ok {
		return Order{}, wrap(ErrMissing, "order %q", orderID)
	}
	return order, nil
}

// Recipe implements Source.
func (g *Graph) Recipe(key Key) (Recipe, error) {
	recipe, ok := g.recipes[key]
	if !ok {
		return Recipe{}, wrap(ErrMissing, "recipe for material %s", key)
	}
	return recipe, nil
}

// Stock implements Source.
func (g *Graph) Stock(key Key) (Stock, error) {
	stock, ok := g.stocks[key]
	if !ok {
		return Stock{}, wrap(ErrMissing, "stock record for leaf material %s", key)
	}
	return stock, nil
}

// Cycle returns a material-dependency cycle among the recipes currently in
// the graph, as a printable path, or the empty string when the known recipes
// are acyclic. Children whose recipe is not yet known are not followed.
func (g *Graph) Cycle() string {
	const (
		unvisited = 0
		active    = 1
		done      = 2
	)
	state := map[Key]int{}
	var stack []Key

	var visit func(key Key) string
	visit = func(key Key) string {
		switch state[key] {
		case active:
			start := 0
			for i, entry := range stack {
				if entry == key {
					start = i
					break
				}
			}
			path := ""
			for _, entry := range stack[start:] {
				path += entry.String() + " -> "
			}
			return path + key.String()
		case done:
			return ""
		}
		state[key] = active
		stack = append(stack, key)
		recipe, ok := g.recipes[key]
		if ok {
			for _, line := range recipe.Lines {
				if _, known := g.recipes[line.Child]; !known {
					continue
				}
				if cycle := visit(line.Child); cycle != "" {
					return cycle
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[key] = done
		return ""
	}

	keys := make([]Key, 0, len(g.recipes))
	for key := range g.recipes {
		keys = append(keys, key)
	}
	sortKeys(keys)
	for _, key := range keys {
		if cycle := visit(key); cycle != "" {
			return cycle
		}
	}
	return ""
}

// Revision returns a counter that increases whenever the graph changes.
// Callers can use it to detect whether an add actually contributed data.
func (g *Graph) Revision() int {
	return g.revision
}

// Recipes returns the recorded recipes sorted by key.
func (g *Graph) Recipes() []Recipe {
	keys := make([]Key, 0, len(g.recipes))
	for key := range g.recipes {
		keys = append(keys, key)
	}
	sortKeys(keys)
	recipes := make([]Recipe, 0, len(keys))
	for _, key := range keys {
		recipes = append(recipes, g.recipes[key])
	}
	return recipes
}

// Stocks returns the recorded stock rows sorted by key.
func (g *Graph) Stocks() []Stock {
	keys := make([]Key, 0, len(g.stocks))
	for key := range g.stocks {
		keys = append(keys, key)
	}
	sortKeys(keys)
	stocks := make([]Stock, 0, len(keys))
	for _, key := range keys {
		stocks = append(stocks, g.stocks[key])
	}
	return stocks
}

// Orders returns the recorded orders sorted by ID.
func (g *Graph) Orders() []Order {
	ids := make([]string, 0, len(g.orders))
	for id := range g.orders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	orders := make([]Order, 0, len(ids))
	for _, id := range ids {
		orders = append(orders, g.orders[id])
	}
	return orders
}

func sortKeys(keys []Key) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Version != keys[j].Version {
			return keys[i].Version < keys[j].Version
		}
		return keys[i].MaterialID < keys[j].MaterialID
	})
}

func validateStock(stock Stock) error {
	if stock.Name == "" {
		return wrap(ErrMissing, "stock record for %s has no name", stock.Key)
	}
	if stock.Supplier == "" {
		return wrap(ErrMissing, "stock record for %s has no supplier", stock.Key)
	}
	if stock.UnitCostCents < 0 {
		return wrap(ErrInvalidQuantity, "stock record for %s has negative unit cost %d", stock.Key, stock.UnitCostCents)
	}
	if stock.OnHand < 0 {
		return wrap(ErrInvalidQuantity, "stock record for %s has negative on-hand %d", stock.Key, stock.OnHand)
	}
	return nil
}

// normalizeLines validates lines and collapses repeats of the same line ID.
func normalizeLines(lines []UsageLine) ([]UsageLine, error) {
	seen := map[string]UsageLine{}
	order := make([]string, 0, len(lines))
	for _, line := range lines {
		if line.ID == "" {
			return nil, wrap(ErrMissing, "usage line with empty line ID")
		}
		if !line.Child.valid() {
			return nil, wrap(ErrMissing, "usage line %q has no child completion version or material ID", line.ID)
		}
		if line.Quantity < 0 {
			return nil, wrap(ErrInvalidQuantity, "usage line %q has negative quantity %d", line.ID, line.Quantity)
		}
		if existing, ok := seen[line.ID]; ok {
			if existing != line {
				return nil, wrap(ErrIdentityConflict, "usage line %q has conflicting payloads", line.ID)
			}
			continue
		}
		seen[line.ID] = line
		order = append(order, line.ID)
	}
	normalized := make([]UsageLine, 0, len(order))
	for _, id := range order {
		normalized = append(normalized, seen[id])
	}
	return normalized, nil
}

// mergeLines merges two already-normalized line sets, detecting conflicts.
func mergeLines(a, b []UsageLine) ([]UsageLine, error) {
	combined := make([]UsageLine, 0, len(a)+len(b))
	combined = append(combined, a...)
	combined = append(combined, b...)
	return normalizeLines(combined)
}

// sameLines reports whether two normalized line sets are identical.
func sameLines(a, b []UsageLine) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
