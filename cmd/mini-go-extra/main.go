// Command mini-go-extra is the central entry point for extra commands of mini-swe-agent-go.
package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/donvito/mini-swe-agent-go/internal/cli"
)

func main() {
	cli.Startup()
	root := &cobra.Command{
		Use:          "mini-go-extra",
		Short:        "Central entry point for all extra commands from mini-swe-agent-go.",
		SilenceUsage: true,
	}
	root.AddCommand(cli.NewConfigCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
