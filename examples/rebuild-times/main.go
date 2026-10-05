// Command svc is a small service that does nothing useful. It exists to
// pull in grpc, cobra and the Prometheus client, so there is something to
// time when one line of it changes.
package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

func main() {
	root := &cobra.Command{
		Use: "svc",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := grpc.NewServer()
			defer srv.Stop()
			http.Handle("/metrics", promhttp.Handler())
			fmt.Println("grpc", grpc.Version)
			return nil
		},
	}
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
