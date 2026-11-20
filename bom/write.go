package bom

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
)

// WriteDetails writes the full purchasing daily detail to w as CSV.
//
// It emits three sections, each with its own header row: the recipe usage
// lines that were read (so the source versions and usage-line IDs are
// preserved), the per-order leaf contributions, and the aggregated purchase
// requirements including zero-purchase rows.
//
// WriteDetails only writes; it never changes inventory.
func (r *Report) WriteDetails(w io.Writer) error {
	buffer := &bytes.Buffer{}
	writer := csv.NewWriter(buffer)

	if err := r.writeDetails(writer); err != nil {
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("bom: failed to write details: %s", err)
	}
	if _, err := w.Write(buffer.Bytes()); err != nil {
		return fmt.Errorf("bom: failed to write details: %s", err)
	}
	return nil
}

func (r *Report) writeDetails(writer *csv.Writer) error {
	write := func(row []string) error {
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("bom: failed to write details: %s", err)
		}
		return nil
	}

	if err := write([]string{"section", "parent_version", "parent_material_id", "usage_line_id", "child_version", "child_material_id", "child_is_leaf", "quantity"}); err != nil {
		return err
	}
	for _, usage := range r.Usage {
		if err := write([]string{
			"usage",
			usage.Parent.Version,
			usage.Parent.MaterialID,
			usage.LineID,
			usage.Child.Version,
			usage.Child.MaterialID,
			strconv.FormatBool(usage.ChildLeaf),
			formatInt(usage.Quantity),
		}); err != nil {
			return err
		}
	}

	if err := write([]string{"section", "order_id", "order_version", "order_material_id", "leaf_version", "leaf_material_id", "leaf_name", "supplier", "quantity", "unit_cost_cents", "cost_cents", "gross_cost_cents"}); err != nil {
		return err
	}
	for _, order := range r.Orders {
		for _, leaf := range order.Leaves {
			if err := write([]string{
				"order_leaf",
				order.Order.ID,
				order.Order.Item.Version,
				order.Order.Item.MaterialID,
				leaf.Leaf.Version,
				leaf.Leaf.MaterialID,
				leaf.Name,
				leaf.Supplier,
				formatInt(leaf.Quantity),
				formatInt(leaf.UnitCostCents),
				formatInt(leaf.CostCents),
				formatInt(order.GrossCostCents),
			}); err != nil {
				return err
			}
		}
	}

	if err := write([]string{"section", "leaf_version", "leaf_material_id", "leaf_name", "supplier", "demand", "on_hand", "net_purchase", "unit_cost_cents", "cost_cents", "total_net_cost_cents"}); err != nil {
		return err
	}
	for _, requirement := range r.Requirements {
		if err := write([]string{
			"requirement",
			requirement.Key.Version,
			requirement.Key.MaterialID,
			requirement.Name,
			requirement.Supplier,
			formatInt(requirement.Demand),
			formatInt(requirement.OnHand),
			formatInt(requirement.NetPurchase),
			formatInt(requirement.UnitCostCents),
			formatInt(requirement.CostCents),
			formatInt(r.NetPurchaseCostCents),
		}); err != nil {
			return err
		}
	}
	return nil
}

// WriteDailyReport writes a human-readable purchasing daily report to w,
// including the per-order section, the net-purchase requirements and the
// supplier rollup.
func (r *Report) WriteDailyReport(w io.Writer) error {
	buffer := &bytes.Buffer{}
	write := func(format string, args ...interface{}) {
		fmt.Fprintf(buffer, format, args...)
	}

	write("Purchasing daily report\n")
	write("=======================\n\n")

	write("Orders\n")
	for _, order := range r.Orders {
		write("  %s  %s @ completion %s  quantity %s  gross cost %s\n",
			order.Order.ID,
			order.Order.Item.MaterialID,
			order.Order.Item.Version,
			formatInt(order.Order.Quantity),
			formatCents(order.GrossCostCents))
		for _, leaf := range order.Leaves {
			write("    %-12s %-14s %-10s qty %6s x %s = %s\n",
				leaf.Leaf.MaterialID,
				leaf.Name,
				leaf.Supplier,
				formatInt(leaf.Quantity),
				formatCents(leaf.UnitCostCents),
				formatCents(leaf.CostCents))
		}
	}
	write("  order gross cost total: %s\n\n", formatCents(r.GrossCostCents))

	write("Net purchases (inventory deducted once)\n")
	for _, requirement := range r.Requirements {
		write("  %-12s %-14s version %-6s demand %6s  on-hand %6s  net %6s  %s\n",
			requirement.Key.MaterialID,
			requirement.Name,
			requirement.Key.Version,
			formatInt(requirement.Demand),
			formatInt(requirement.OnHand),
			formatInt(requirement.NetPurchase),
			formatCents(requirement.CostCents))
	}
	write("  net purchase cost total: %s\n\n", formatCents(r.NetPurchaseCostCents))

	write("Suppliers\n")
	for _, supplier := range r.Suppliers {
		write("  %s  net %6s  cost %s\n",
			supplier.Supplier,
			formatInt(supplier.NetPurchase),
			formatCents(supplier.CostCents))
		for _, line := range supplier.Lines {
			write("    %-12s %-14s net %6s x %s = %s\n",
				line.Key.MaterialID,
				line.Name,
				formatInt(line.NetPurchase),
				formatCents(line.UnitCostCents),
				formatCents(line.CostCents))
		}
	}

	if _, err := w.Write(buffer.Bytes()); err != nil {
		return fmt.Errorf("bom: failed to write report: %s", err)
	}
	return nil
}

func formatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}

// formatCents renders cents as a fixed-point amount, without floating point
// arithmetic.
func formatCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return sign + strconv.FormatInt(cents/100, 10) + "." + pad2(cents%100)
}

func pad2(value int64) string {
	if value < 10 {
		return "0" + strconv.FormatInt(value, 10)
	}
	return strconv.FormatInt(value, 10)
}
