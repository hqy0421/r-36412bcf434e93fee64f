// Package bom collects bill-of-materials (BOM) requirements from an already
// logged-in web page and turns them into a purchasing daily report.
//
// The collector expands the BOM that is reachable from a given order, reading
// complete recipes, integer usage quantities, leaf-material stock and costs in
// cents. It is deliberately read-only: a Source is never asked to change
// inventory, and the Agouti-backed PageSource only clicks expand controls.
//
// # Planning rules
//
// A recipe is identified by its completion version and material ID. Each usage
// line inside a recipe is identified by its usage-line ID. The same usage line
// may be shown repeatedly by the page (for example when a shared sub-assembly
// is re-rendered); repeated copies of the same line are counted once. Distinct
// usage lines and distinct paths through the BOM are all accumulated.
//
// Leaf quantities are first summed into an order contribution. Inventory is
// then deducted exactly once per (completion version, material ID) from the
// aggregate demand across all planned orders, producing net purchases and a
// supplier report. Zero-purchase rows are retained, as are the source versions
// and IDs.
//
// The planner fails explicitly on cycles, missing materials or stock records,
// conflicting identities, empty expansion regions (which are never treated as
// leaf materials) and arithmetic overflow.
//
// # Page contract
//
// PageSource reads a DOM contract whose selectors and attribute names are
// intentionally small. All of them are configurable through Selectors; the
// defaults are:
//
//	order root:   .bom-order
//	              data-order-id, data-version, data-material-id, data-quantity
//	BOM node:     .bom-node
//	              data-version, data-material-id, data-leaf, data-expanded
//	usage lines:  .bom-lines, containing .bom-line rows
//	              data-line-id, data-child-version,
//	              data-child-material-id, data-quantity
//	stock record: .bom-stock
//	              data-version, data-material-id, data-name, data-supplier,
//	              data-unit-cost-cents, data-on-hand
//	expander:     .bom-expand (inside a BOM node)
//	loading area: .bom-loading (inside a BOM node; only a visible placeholder
//	              counts as loading)
//
// A node marked data-leaf="true" is a leaf. A non-leaf node is expanded by
// clicking its expand control and waiting for the loading area to clear. A
// node that has no lines once its expansion has finished is an empty
// expansion, not a leaf, and fails.
//
// Collection is a fixpoint: expanding a material can render further nodes,
// including another copy of a recipe already seen. Every rendered copy of a
// reachable material is expanded and merged, so a shared sub-assembly that the
// page renders many times contributes its usage lines exactly once.
package bom
