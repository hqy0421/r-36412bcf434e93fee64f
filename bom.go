package agouti

import (
	"io"

	"github.com/sclevine/agouti/bom"
)

// The bom package expands a bill of materials from an already logged-in page
// and produces a purchasing daily report. These functions are thin helpers
// that operate on a *Page directly.
//
// They are read-only: collecting the BOM clicks the page's own expand
// controls but never changes remote inventory.

// CollectBOM expands the BOM reachable from the given orders on an already
// logged-in page and plans the purchasing requirements. It returns the report
// without writing any output.
//
// See github.com/sclevine/agouti/bom for the DOM contract and planning rules.
func (p *Page) CollectBOM(orderIDs []string) (*bom.Report, error) {
	return bom.PageDaily(p, orderIDs, nil, nil)
}

// PurchasingDaily expands the BOM reachable from the given orders on an
// already logged-in page, writes a detail CSV to details and a human-readable
// daily report to report, and returns the planning result. Either writer may
// be nil to skip that output.
func (p *Page) PurchasingDaily(orderIDs []string, details, report io.Writer) (*bom.Report, error) {
	return bom.PageDaily(p, orderIDs, details, report)
}
