package bom

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sclevine/agouti/api"
)

// Page is the part of an Agouti page the collector needs. *agouti.Page
// satisfies it directly, so callers normally pass an already logged-in page.
type Page interface {
	Session() *api.Session
}

// Selectors and attributes used by PageSource. They are exported so that
// callers can adapt PageSource to an existing page without forking the
// package, but the defaults match the contract described in the package
// documentation.
type Selectors struct {
	Order    string // elements carrying the order ID
	Node     string // per-material recipe nodes
	Expander string // clickable expand control inside a node
	Loading  string // loading placeholder inside a node
	LineList string // container of usage lines inside a node
	Line     string // usage line elements inside LineList
	Stock    string // leaf stock rows

	OrderID      string // attribute on Order elements
	Version      string // completion version attribute
	MaterialID   string // material ID attribute
	Leaf         string // "true" when a node is a leaf material
	Expanded     string // "true" once a node has been expanded
	Quantity     string // order and usage-line quantity attribute
	LineID       string // usage-line ID attribute
	ChildVersion string // child completion version on a usage line
	ChildID      string // child material ID on a usage line

	Name     string // stock display name attribute
	Supplier string // stock supplier attribute
	UnitCost string // stock unit cost in cents attribute
	OnHand   string // stock on-hand quantity attribute
}

// DefaultSelectors is the DOM contract used when PageSource.Selectors is nil.
var DefaultSelectors = Selectors{
	Order:    ".bom-order",
	Node:     ".bom-node",
	Expander: ".bom-expand",
	Loading:  ".bom-loading",
	LineList: ".bom-lines",
	Line:     ".bom-line",
	Stock:    ".bom-stock",

	OrderID:      "data-order-id",
	Version:      "data-version",
	MaterialID:   "data-material-id",
	Leaf:         "data-leaf",
	Expanded:     "data-expanded",
	Quantity:     "data-quantity",
	LineID:       "data-line-id",
	ChildVersion: "data-child-version",
	ChildID:      "data-child-material-id",

	Name:     "data-name",
	Supplier: "data-supplier",
	UnitCost: "data-unit-cost-cents",
	OnHand:   "data-on-hand",
}

// PageSource reads BOM records from an already logged-in Agouti page by
// expanding the order's reachable materials.
//
// Collect walks the page once: Order, Recipe and Stock then answer from the
// collected snapshot. Every expansion is performed by clicking the page's own
// expand control and waiting for its loading placeholder to clear. PageSource
// never writes to the page's inventory.
type PageSource struct {
	Page      Page
	Selectors Selectors
	Timeout   time.Duration

	graph     *Graph
	collected bool
}

// NewPageSource returns a PageSource using the default selectors.
func NewPageSource(page Page) *PageSource {
	return &PageSource{Page: page, Timeout: 10 * time.Second}
}

func (s *PageSource) settings() Selectors {
	selectors := s.Selectors
	defaults := DefaultSelectors

	setIfEmpty(&selectors.Order, defaults.Order)
	setIfEmpty(&selectors.Node, defaults.Node)
	setIfEmpty(&selectors.Expander, defaults.Expander)
	setIfEmpty(&selectors.Loading, defaults.Loading)
	setIfEmpty(&selectors.LineList, defaults.LineList)
	setIfEmpty(&selectors.Line, defaults.Line)
	setIfEmpty(&selectors.Stock, defaults.Stock)
	setIfEmpty(&selectors.OrderID, defaults.OrderID)
	setIfEmpty(&selectors.Version, defaults.Version)
	setIfEmpty(&selectors.MaterialID, defaults.MaterialID)
	setIfEmpty(&selectors.Leaf, defaults.Leaf)
	setIfEmpty(&selectors.Expanded, defaults.Expanded)
	setIfEmpty(&selectors.Quantity, defaults.Quantity)
	setIfEmpty(&selectors.LineID, defaults.LineID)
	setIfEmpty(&selectors.ChildVersion, defaults.ChildVersion)
	setIfEmpty(&selectors.ChildID, defaults.ChildID)
	setIfEmpty(&selectors.Name, defaults.Name)
	setIfEmpty(&selectors.Supplier, defaults.Supplier)
	setIfEmpty(&selectors.UnitCost, defaults.UnitCost)
	setIfEmpty(&selectors.OnHand, defaults.OnHand)
	return selectors
}

func setIfEmpty(field *string, value string) {
	if *field == "" {
		*field = value
	}
}

// Collect expands the BOM reachable from the given orders and snapshots all
// recipes and leaf stock records. It must be called before Order, Recipe or
// Stock.
//
// Collection is a fixpoint over the materials reachable from the requested
// orders: expanding one material can render other materials, including
// further copies of recipes already seen. Every rendered copy of a reachable
// material is expanded and merged, and the same usage line is counted once no
// matter how often the page renders it.
func (s *PageSource) Collect(orderIDs []string) error {
	if s.Page == nil {
		return wrap(ErrMissing, "no page")
	}
	selectors := s.settings()
	graph := NewGraph()
	orderKeys := map[string]Key{}
	reachable := map[Key]bool{}

	for _, orderID := range orderIDs {
		if orderID == "" {
			return wrap(ErrMissing, "order ID is empty")
		}
		if _, ok := orderKeys[orderID]; ok {
			continue
		}
		order, err := s.readOrder(selectors, orderID)
		if err != nil {
			return err
		}
		if err := graph.AddOrder(order); err != nil {
			return err
		}
		orderKeys[orderID] = order.Item
		reachable[order.Item] = true
	}

	handled := map[string]bool{}
	for round := 0; ; round++ {
		if round >= maxExpansionRounds {
			return wrap(ErrCycle, "page kept rendering new material copies")
		}

		progress := false
		newKeys := false
		revision := graph.Revision()
		keys := sortedKeys(reachable)
		for _, key := range keys {
			nodes, err := s.findNodes(selectors, key)
			if err != nil {
				return err
			}

			var fresh []*api.Element
			for _, node := range nodes {
				if !handled[node.ID] {
					fresh = append(fresh, node)
				}
			}
			if len(fresh) == 0 {
				continue
			}
			progress = true

			children, err := s.expandCopies(selectors, graph, key, fresh)
			if err != nil {
				return err
			}
			handledFor(fresh, handled)
			for _, child := range children {
				if !reachable[child] {
					reachable[child] = true
					newKeys = true
				}
			}
		}

		if !progress {
			break
		}
		// A page that re-renders identical copies of already-known material
		// makes no progress: there are no new reachable materials and the
		// graph did not change. Stop instead of chasing fresh element IDs.
		if !newKeys && graph.Revision() == revision {
			break
		}
	}

	for _, key := range sortedKeys(reachable) {
		if _, err := graph.Recipe(key); err != nil {
			return wrap(ErrMissing, "material %s is not rendered", key)
		}
	}
	if cycle := graph.Cycle(); cycle != "" {
		return wrap(ErrCycle, "%s", cycle)
	}

	s.graph = graph
	s.collected = true
	return nil
}

// maxExpansionRounds bounds the fixpoint so that a page that keeps rendering
// new copies of the same material fails instead of looping forever.
const maxExpansionRounds = 1000

func handledFor(nodes []*api.Element, handled map[string]bool) {
	for _, node := range nodes {
		handled[node.ID] = true
	}
}

func sortedKeys(keys map[Key]bool) []Key {
	result := make([]Key, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sortKeys(result)
	return result
}

// Order implements Source.
func (s *PageSource) Order(orderID string) (Order, error) {
	if err := s.requireCollected(); err != nil {
		return Order{}, err
	}
	return s.graph.Order(orderID)
}

// Recipe implements Source.
func (s *PageSource) Recipe(key Key) (Recipe, error) {
	if err := s.requireCollected(); err != nil {
		return Recipe{}, err
	}
	return s.graph.Recipe(key)
}

// Stock implements Source.
func (s *PageSource) Stock(key Key) (Stock, error) {
	if err := s.requireCollected(); err != nil {
		return Stock{}, err
	}
	return s.graph.Stock(key)
}

// Graph returns the collected snapshot. It is nil before Collect succeeds.
func (s *PageSource) Graph() *Graph {
	return s.graph
}

func (s *PageSource) requireCollected() error {
	if !s.collected {
		return wrap(ErrMissing, "page source has not been collected")
	}
	return nil
}

func (s *PageSource) session() *api.Session {
	return s.Page.Session()
}

func cssSelector(value string) api.Selector {
	return api.Selector{Using: "css selector", Value: value}
}

func findAll(client interface {
	GetElements(selector api.Selector) ([]*api.Element, error)
}, selector string) ([]*api.Element, error) {
	return client.GetElements(cssSelector(selector))
}

func (s *PageSource) readOrder(selectors Selectors, orderID string) (Order, error) {
	elements, err := findAll(s.session(), selectors.Order)
	if err != nil {
		return Order{}, fmt.Errorf("bom: failed to read orders: %s", err)
	}

	var (
		order Order
		found bool
	)
	for _, element := range elements {
		id, err := element.GetAttribute(selectors.OrderID)
		if err != nil {
			return Order{}, fmt.Errorf("bom: failed to read order id: %s", err)
		}
		if strings.TrimSpace(id) != orderID {
			continue
		}

		version, err := element.GetAttribute(selectors.Version)
		if err != nil {
			return Order{}, fmt.Errorf("bom: failed to read order version: %s", err)
		}
		materialID, err := element.GetAttribute(selectors.MaterialID)
		if err != nil {
			return Order{}, fmt.Errorf("bom: failed to read order material: %s", err)
		}
		quantityText, err := element.GetAttribute(selectors.Quantity)
		if err != nil {
			return Order{}, fmt.Errorf("bom: failed to read order quantity: %s", err)
		}
		quantity, err := parseQuantity(quantityText, "order "+orderID)
		if err != nil {
			return Order{}, err
		}

		candidate := Order{
			ID:       orderID,
			Item:     Key{Version: strings.TrimSpace(version), MaterialID: strings.TrimSpace(materialID)},
			Quantity: quantity,
		}
		if found && candidate != order {
			return Order{}, wrap(ErrIdentityConflict, "order %q is rendered with different contents", orderID)
		}
		order = candidate
		found = true
	}

	if !found {
		return Order{}, wrap(ErrMissing, "order %q", orderID)
	}
	return order, nil
}

// expandCopies expands the given copies of one material's node, merges what
// they reveal into the graph and returns the child materials they reference.
// A material rendered as both leaf and non-leaf, or a leaf rendered with
// usage lines, is an identity conflict.
func (s *PageSource) expandCopies(selectors Selectors, graph *Graph, key Key, nodes []*api.Element) ([]Key, error) {
	var children []Key
	leaf := false
	leafSet := false

	for _, node := range nodes {
		nodeLeaf, err := s.expandNode(selectors, node)
		if err != nil {
			return nil, err
		}
		if leafSet && nodeLeaf != leaf {
			return nil, wrap(ErrIdentityConflict, "material %s is rendered as both leaf and non-leaf", key)
		}
		leaf, leafSet = nodeLeaf, true

		lines, err := s.readLines(selectors, node)
		if err != nil {
			return nil, err
		}
		if leaf {
			if len(lines) > 0 {
				return nil, wrap(ErrIdentityConflict, "leaf material %s is rendered with %d usage lines", key, len(lines))
			}
			if err := s.readStock(selectors, graph, key); err != nil {
				return nil, err
			}
		} else {
			if len(lines) == 0 {
				// The loading placeholder has cleared but no recipe was
				// revealed. This is an empty expansion, never a leaf. It is
				// only tolerable when another copy of the same material has
				// already produced its lines.
				existing, err := graph.Recipe(key)
				if err != nil || existing.Leaf || len(existing.Lines) == 0 {
					return nil, wrap(ErrEmptyExpansion, "material %s expanded to an empty region", key)
				}
				continue
			}
			children = append(children, lineChildren(lines)...)
		}

		if err := graph.AddRecipe(Recipe{Key: key, Leaf: leaf, Lines: lines}); err != nil {
			return nil, err
		}
	}
	return children, nil
}

func lineChildren(lines []UsageLine) []Key {
	seen := map[string]bool{}
	var children []Key
	for _, line := range lines {
		if seen[line.ID] {
			continue
		}
		seen[line.ID] = true
		children = append(children, line.Child)
	}
	return children
}

// findNodes returns every rendered copy of key's node. The match is scoped by
// attribute selector so that a large page is filtered by the browser rather
// than by one attribute round-trip per node.
func (s *PageSource) findNodes(selectors Selectors, key Key) ([]*api.Element, error) {
	selector := fmt.Sprintf("%s[%s=%s][%s=%s]",
		selectors.Node,
		selectors.Version, cssString(key.Version),
		selectors.MaterialID, cssString(key.MaterialID),
	)
	elements, err := findAll(s.session(), selector)
	if err != nil {
		return nil, fmt.Errorf("bom: failed to read material nodes: %s", err)
	}
	return elements, nil
}

// cssString quotes a value for use inside a CSS attribute selector.
func cssString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}

// expandNode clicks the node's expand control (when it has one) and waits for
// the loading placeholder to clear. It returns whether the node is a leaf.
func (s *PageSource) expandNode(selectors Selectors, node *api.Element) (bool, error) {
	leafText, err := node.GetAttribute(selectors.Leaf)
	if err != nil {
		return false, fmt.Errorf("bom: failed to read leaf flag: %s", err)
	}
	leaf := strings.TrimSpace(strings.ToLower(leafText)) == "true"

	expandedText, err := node.GetAttribute(selectors.Expanded)
	if err != nil {
		return false, fmt.Errorf("bom: failed to read expanded flag: %s", err)
	}
	alreadyExpanded := strings.TrimSpace(strings.ToLower(expandedText)) == "true"

	if leaf {
		// A leaf has nothing to expand; its stock record is read directly.
		return true, nil
	}

	if !alreadyExpanded {
		expanders, err := findAll(node, selectors.Expander)
		if err != nil {
			return false, fmt.Errorf("bom: failed to find expand control: %s", err)
		}
		if len(expanders) == 0 {
			return false, wrap(ErrMissing, "non-leaf material has no expand control")
		}
		if err := expanders[0].Click(); err != nil {
			return false, fmt.Errorf("bom: failed to expand material: %s", err)
		}
	}

	if err := s.waitForExpansion(selectors, node, leaf); err != nil {
		return false, err
	}
	return leaf, nil
}

func (s *PageSource) waitForExpansion(selectors Selectors, node *api.Element, leaf bool) error {
	deadline := time.Now().Add(s.Timeout)
	var lastErr error
	sawLoading := false

	for {
		loading, err := anyDisplayed(node, selectors.Loading)
		if err != nil {
			lastErr = err
			loading = false
		}
		if loading {
			sawLoading = true
		}

		lines := 0
		if !leaf {
			var linesErr error
			lines, linesErr = s.countLines(selectors, node)
			if linesErr != nil {
				lastErr = linesErr
			}
		}

		if !loading {
			if leaf {
				return nil
			}
			expanded, err := node.GetAttribute(selectors.Expanded)
			switch {
			case err != nil:
				lastErr = err
			case strings.TrimSpace(strings.ToLower(expanded)) == "true":
				// Expansion has settled. An empty line list is reported by
				// readLines as an empty expansion, never as a leaf.
				return nil
			case lines > 0:
				return nil
			case sawLoading:
				// The loading placeholder was shown and has now cleared, so
				// the expansion finished even if the page did not set its
				// expanded flag. An empty line list is an empty expansion.
				return nil
			}
		}

		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("bom: failed to wait for expansion: %s", lastErr)
			}
			return wrap(ErrTimeout, "material did not finish expanding")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// anyDisplayed reports whether any element matching selector is visible. A
// loading placeholder that is present but hidden is not loading.
func anyDisplayed(parent *api.Element, selector string) (bool, error) {
	elements, err := findAll(parent, selector)
	if err != nil {
		return false, err
	}
	for _, element := range elements {
		displayed, err := element.IsDisplayed()
		if err != nil {
			return false, err
		}
		if displayed {
			return true, nil
		}
	}
	return false, nil
}

func (s *PageSource) countLines(selectors Selectors, node *api.Element) (int, error) {
	lists, err := findAll(node, selectors.LineList)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, list := range lists {
		lines, err := findAll(list, selectors.Line)
		if err != nil {
			return 0, err
		}
		total += len(lines)
	}
	return total, nil
}

func (s *PageSource) readLines(selectors Selectors, node *api.Element) ([]UsageLine, error) {
	lists, err := findAll(node, selectors.LineList)
	if err != nil {
		return nil, fmt.Errorf("bom: failed to read usage lines: %s", err)
	}
	if len(lists) > 1 {
		return nil, wrap(ErrIdentityConflict, "material node has %d usage-line regions", len(lists))
	}
	if len(lists) == 0 {
		return nil, nil
	}

	lines, err := findAll(lists[0], selectors.Line)
	if err != nil {
		return nil, fmt.Errorf("bom: failed to read usage lines: %s", err)
	}

	result := make([]UsageLine, 0, len(lines))
	for _, line := range lines {
		lineID, err := line.GetAttribute(selectors.LineID)
		if err != nil {
			return nil, fmt.Errorf("bom: failed to read usage-line id: %s", err)
		}
		childVersion, err := line.GetAttribute(selectors.ChildVersion)
		if err != nil {
			return nil, fmt.Errorf("bom: failed to read usage-line child version: %s", err)
		}
		childID, err := line.GetAttribute(selectors.ChildID)
		if err != nil {
			return nil, fmt.Errorf("bom: failed to read usage-line child material: %s", err)
		}
		quantityText, err := line.GetAttribute(selectors.Quantity)
		if err != nil {
			return nil, fmt.Errorf("bom: failed to read usage-line quantity: %s", err)
		}
		quantity, err := parseQuantity(quantityText, "usage line "+strings.TrimSpace(lineID))
		if err != nil {
			return nil, err
		}

		result = append(result, UsageLine{
			ID:       strings.TrimSpace(lineID),
			Child:    Key{Version: strings.TrimSpace(childVersion), MaterialID: strings.TrimSpace(childID)},
			Quantity: quantity,
		})
	}
	return result, nil
}

func (s *PageSource) readStock(selectors Selectors, graph *Graph, key Key) error {
	selector := fmt.Sprintf("%s[%s=%s][%s=%s]",
		selectors.Stock,
		selectors.Version, cssString(key.Version),
		selectors.MaterialID, cssString(key.MaterialID),
	)
	elements, err := findAll(s.session(), selector)
	if err != nil {
		return fmt.Errorf("bom: failed to read stock rows: %s", err)
	}

	found := false
	var record Stock
	for _, element := range elements {
		name, err := element.GetAttribute(selectors.Name)
		if err != nil {
			return fmt.Errorf("bom: failed to read stock name: %s", err)
		}
		supplier, err := element.GetAttribute(selectors.Supplier)
		if err != nil {
			return fmt.Errorf("bom: failed to read stock supplier: %s", err)
		}
		costText, err := element.GetAttribute(selectors.UnitCost)
		if err != nil {
			return fmt.Errorf("bom: failed to read stock unit cost: %s", err)
		}
		onHandText, err := element.GetAttribute(selectors.OnHand)
		if err != nil {
			return fmt.Errorf("bom: failed to read stock on-hand: %s", err)
		}
		cost, err := parseQuantity(costText, "stock unit cost for "+key.String())
		if err != nil {
			return err
		}
		onHand, err := parseQuantity(onHandText, "stock on-hand for "+key.String())
		if err != nil {
			return err
		}

		candidate := Stock{
			Key:           key,
			Name:          strings.TrimSpace(name),
			Supplier:      strings.TrimSpace(supplier),
			UnitCostCents: cost,
			OnHand:        onHand,
		}
		if found && candidate != record {
			return wrap(ErrIdentityConflict, "leaf material %s has conflicting stock records", key)
		}
		record = candidate
		found = true
	}

	if !found {
		return wrap(ErrMissing, "stock record for leaf material %s", key)
	}
	return graph.AddStock(record)
}

func parseQuantity(text, label string) (int64, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, wrap(ErrMissing, "%s has no quantity", label)
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		if numErr, ok := err.(*strconv.NumError); ok && numErr.Err == strconv.ErrRange {
			return 0, wrap(ErrOverflow, "%s quantity %q overflows int64", label, trimmed)
		}
		return 0, wrap(ErrInvalidQuantity, "%s quantity %q is not an integer", label, trimmed)
	}
	if value < 0 {
		return 0, wrap(ErrInvalidQuantity, "%s quantity %d is negative", label, value)
	}
	return value, nil
}
