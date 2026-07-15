// healthcheck.go
package cli

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/luisfelipecoelho/go-template/internal/config"
)

// healthCheckTimeout bounds the probe so a Docker HEALTHCHECK never stalls.
const healthCheckTimeout = 2 * time.Second

// newHealthcheckCmd probes the local HTTP server's /healthz endpoint and exits
// non-zero on any non-200 response or connection failure. The server route is
// delivered in Epic 2; the command is usable now for Docker HEALTHCHECK.
func newHealthcheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "healthcheck",
		Short: "Probe the local /healthz endpoint (exit 0 on 200)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading healthcheck config: %w", err)
			}
			// URL is built from the validated port config, never user input.
			url := fmt.Sprintf("http://localhost:%d/healthz", cfg.HTTPPort)
			return checkHealth(cmd.Context(), url, healthClient())
		},
	}
}

// healthClient returns a bounded HTTP client that does not follow redirects; a
// health probe must observe the endpoint's own status.
func healthClient() *http.Client {
	return &http.Client{
		Timeout: healthCheckTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// checkHealth performs a single GET and returns nil only on HTTP 200.
func checkHealth(ctx context.Context, url string, client *http.Client) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("building healthcheck request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck request: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("closing healthcheck body: %w", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: unexpected status %d", resp.StatusCode)
	}
	return nil
}
