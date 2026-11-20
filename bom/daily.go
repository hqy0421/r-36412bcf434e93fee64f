package bom

import "io"

// Daily collects the BOM for the given orders from source, plans the
// purchasing requirements, writes the detail CSV to details and the
// human-readable daily report to report, and returns the planning result.
//
// Either writer may be nil to skip that output.
func Daily(source Source, orderIDs []string, details, report io.Writer) (*Report, error) {
	if source == nil {
		return nil, wrap(ErrMissing, "no BOM source")
	}
	result, err := Run(source, orderIDs)
	if err != nil {
		return nil, err
	}
	if details != nil {
		if err := result.WriteDetails(details); err != nil {
			return nil, err
		}
	}
	if report != nil {
		if err := result.WriteDailyReport(report); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// PageDaily is Daily for an already logged-in Agouti page. It expands the
// page's BOM once, then plans from the collected snapshot.
func PageDaily(page Page, orderIDs []string, details, report io.Writer) (*Report, error) {
	source := NewPageSource(page)
	if err := source.Collect(orderIDs); err != nil {
		return nil, err
	}
	return Daily(source, orderIDs, details, report)
}
