// root.go
// Package cli assembles the single-binary cobra command tree: root plus the
// server, worker, version, and healthcheck subcommands. Each subcommand is
// constructed by its own newXCmd function; Execute wires them together.
//
// Security: no secrets are embedded here. Version metadata is injected at build
// time via -ldflags and defaults to "dev".
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Execute builds the root command and runs it against os.Args, returning any
// error to main for a non-zero exit.
func Execute() error {
	if err := newRootCmd().Execute(); err != nil {
		return fmt.Errorf("executing command: %w", err)
	}
	return nil
}

// newRootCmd assembles the root command and its subcommands.
//
// cobra remains the sole error printer: for argument/unknown-command failures it
// writes the error plus usage guidance to stderr. Each RunE sets
// SilenceUsage=true so runtime (non-argument) failures print a clean error with
// no usage block. main only maps a returned error to a non-zero exit.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "app",
		Short: "go-template single binary (server, worker, version, healthcheck)",
	}
	root.AddCommand(
		newServerCmd(),
		newWorkerCmd(),
		newVersionCmd(),
		newHealthcheckCmd(),
	)
	return root
}
