package bom

// OrderLeaf is one order's contribution for one leaf material, before any
// inventory is deducted. Quantity is in whole units; CostCents is the gross
// cost (quantity times unit cost) in cents.
type OrderLeaf struct {
	OrderID       string
	Leaf          Key
	Name          string
	Supplier      string
	Quantity      int64
	UnitCostCents int64
	CostCents     int64
}

// OrderReport is the expanded result for a single order: its leaf
// contributions and gross cost.
type OrderReport struct {
	Order          Order
	Leaves         []OrderLeaf
	GrossCostCents int64
}

// Requirement is the aggregate demand for one leaf material across every
// planned order, with inventory deducted exactly once.
type Requirement struct {
	Key           Key
	Name          string
	Supplier      string
	UnitCostCents int64
	Demand        int64
	OnHand        int64
	NetPurchase   int64
	CostCents     int64
}

// SupplierLine is one leaf material purchased from a supplier.
type SupplierLine struct {
	Key           Key
	Name          string
	NetPurchase   int64
	UnitCostCents int64
	CostCents     int64
}

// SupplierReport groups net purchases by supplier.
type SupplierReport struct {
	Supplier    string
	Lines       []SupplierLine
	NetPurchase int64
	CostCents   int64
}

// UsageRow records one distinct usage line that was read from the source, so
// that the report can show the recipe structure and the source IDs.
type UsageRow struct {
	Parent    Key
	LineID    string
	Child     Key
	Quantity  int64
	ChildLeaf bool
}

// Report is the purchasing daily report.
//
// GrossCostCents is the cost of all leaf material contributed by all orders,
// before inventory is deducted. NetPurchaseCostCents is the cost of only the
// quantity that must actually be purchased after stock is applied once.
type Report struct {
	Orders               []OrderReport
	Requirements         []Requirement
	Suppliers            []SupplierReport
	Usage                []UsageRow
	GrossCostCents       int64
	NetPurchaseCostCents int64
}
