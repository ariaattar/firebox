package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

func newMetricsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "metrics",
		Short: "Show latency metrics",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, ctx, cancel, err := daemonClient()
			if err != nil {
				return err
			}
			defer cancel()
			resp, err := client.Metrics(ctx)
			if err != nil {
				return err
			}
			type row struct {
				op string
				v  struct {
					Count int
					P50Ms float64
					P95Ms float64
					P99Ms float64
					MaxMs float64
				}
			}
			rows := make([]row, 0, len(resp.Operations))
			for op, v := range resp.Operations {
				r := row{op: op}
				r.v.Count = v.Count
				r.v.P50Ms = v.P50Ms
				r.v.P95Ms = v.P95Ms
				r.v.P99Ms = v.P99Ms
				r.v.MaxMs = v.MaxMs
				rows = append(rows, r)
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].op < rows[j].op })
			fmt.Println("OP\tCOUNT\tP50MS\tP95MS\tP99MS\tMAXMS")
			for _, r := range rows {
				fmt.Printf("%s\t%d\t%.2f\t%.2f\t%.2f\t%.2f\n", r.op, r.v.Count, r.v.P50Ms, r.v.P95Ms, r.v.P99Ms, r.v.MaxMs)
			}
			return nil
		},
	}
}
