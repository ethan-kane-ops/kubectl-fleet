package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
)

// namesPerCause is how many contexts the brief summary names per cause.
const namesPerCause = 2

// reportFailures tells the user which contexts failed. Brief (default):
// one line grouping failures by cause, largest group first, naming a few
// contexts each. Detailed (--errors): one line per failed context with the
// full error. A --where reachable skip count follows either way.
func reportFailures(w io.Writer, total int, failures []fleet.Failure, skipped int, detailed bool) {
	sorted := append([]fleet.Failure(nil), failures...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Context < sorted[j].Context })
	switch {
	case len(sorted) == 0:
	case detailed:
		for _, f := range sorted {
			_, _ = fmt.Fprintf(w, "warn: %s: %v\n", f.Context, f.Err)
		}
	default:
		byCause := map[string][]string{}
		for _, f := range sorted {
			c := fleet.Cause(f.Err)
			byCause[c] = append(byCause[c], f.Context)
		}
		causes := make([]string, 0, len(byCause))
		for c := range byCause {
			causes = append(causes, c)
		}
		sort.Slice(causes, func(i, j int) bool {
			if a, b := len(byCause[causes[i]]), len(byCause[causes[j]]); a != b {
				return a > b
			}
			return causes[i] < causes[j]
		})
		groups := make([]string, 0, len(causes))
		for _, c := range causes {
			names := byCause[c]
			shown := names[:min(len(names), namesPerCause)]
			list := strings.Join(shown, ", ")
			if more := len(names) - len(shown); more > 0 {
				list += fmt.Sprintf(", +%d", more)
			}
			groups = append(groups, fmt.Sprintf("%d %s (%s)", len(names), c, list))
		}
		_, _ = fmt.Fprintf(w, "warn: %d of %d contexts failed: %s. Use --errors for details.\n", len(sorted), total, strings.Join(groups, ", "))
	}
	if skipped > 0 {
		_, _ = fmt.Fprintf(w, "warn: skipped %d unreachable contexts (--where reachable).\n", skipped)
	}
}

// promoteErrorColumn moves a table's last wide column (ERROR) into the
// regular columns, so --errors shows failure text without -o wide.
func promoteErrorColumn(tbl *output.Table) {
	n := len(tbl.WideHeaders)
	if n == 0 || tbl.WideHeaders[n-1] != "ERROR" {
		return
	}
	tbl.Headers = append(tbl.Headers, "ERROR")
	tbl.WideHeaders = tbl.WideHeaders[:n-1]
	for i := range tbl.Rows {
		cell := ""
		if i < len(tbl.WideRows) && len(tbl.WideRows[i]) == n {
			cell = tbl.WideRows[i][n-1]
			tbl.WideRows[i] = tbl.WideRows[i][:n-1]
		}
		tbl.Rows[i] = append(tbl.Rows[i], cell)
	}
}
