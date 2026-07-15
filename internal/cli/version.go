// version.go
package cli

import (
	"github.com/spf13/cobra"
)

// Build metadata, injected via:
//
//	-ldflags "-X github.com/luisfelipecoelho/go-template/internal/cli.version=..."
//
// They default to "dev" for local builds where ldflags are absent.
var (
	version   = "dev"
	commit    = "dev"
	buildTime = "dev"
)

// newVersionCmd prints the build metadata.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, and build time",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			cmd.Printf("version: %s\ncommit: %s\nbuild time: %s\n", version, commit, buildTime)
			return nil
		},
	}
}
