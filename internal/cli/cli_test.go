// commands_test.go
package cli

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luisfelipecoelho/go-template/internal/config"
)

// runRoot executes a fresh root command with args, capturing combined output.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestRoot_HelpListsAllSubcommands(t *testing.T) {
	out, err := runRoot(t, "--help")
	require.NoError(t, err)
	for _, sub := range []string{"server", "worker", "version", "healthcheck"} {
		assert.Contains(t, out, sub, "--help must list %q", sub)
	}
}

func TestVersion_PrintsBuildInfo(t *testing.T) {
	out, err := runRoot(t, "version")
	require.NoError(t, err)
	// Defaults are "dev" when ldflags are absent; all three must be printed.
	assert.Contains(t, out, version)
	assert.Contains(t, out, commit)
	assert.Contains(t, out, buildTime)
	assert.Contains(t, out, "version")
	assert.Contains(t, out, "commit")
}

func TestUnknownCommand_ErrorsWithUsage(t *testing.T) {
	// cobra writes the error plus usage guidance to stderr; the non-zero exit is
	// represented by the returned error.
	out, err := runRoot(t, "definitely-not-a-command")
	require.Error(t, err, "unknown command must return an error (non-zero exit)")
	assert.Contains(t, err.Error(), "unknown command")
	assert.Contains(t, out, "unknown command", "error must be printed to stderr")
	assert.Contains(t, out, "usage", "usage guidance must be printed to stderr")
}

func TestServer_InvalidConfigReturnsError(t *testing.T) {
	t.Setenv("HTTP_PORT", "not-a-number")
	out, err := runRoot(t, "server")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP_PORT")
	assert.NotContains(t, out, "Usage:", "runtime config error must not print usage")
}

func TestWorker_InvalidConfigReturnsError(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")
	_, err := runRoot(t, "worker")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LOG_LEVEL")
}

func TestRunServer_StartsAndDrainsOnCancel(t *testing.T) {
	t.Setenv("HTTP_PORT", strconv.Itoa(freePort(t))) // avoid a fixed-port conflict
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServer(ctx, validConfig()) }()

	time.Sleep(100 * time.Millisecond) // let the server bind + serve
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err, "server must drain and return nil on cancel")
	case <-time.After(3 * time.Second):
		t.Fatal("runServer did not return after cancel")
	}
}

func TestRunWorker_StartsAndDrainsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWorker(ctx, validConfig()) }()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err, "worker must drain and return nil on cancel")
	case <-time.After(3 * time.Second):
		t.Fatal("runWorker did not return after cancel")
	}
}

func TestExecute_RunsRealRoot(t *testing.T) {
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	os.Args = []string{"app", "version"}
	assert.NoError(t, Execute())
}

func TestExecute_ErrorPathWraps(t *testing.T) {
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	os.Args = []string{"app", "definitely-not-a-command"}
	err := Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "executing command")
}

func TestServerCommand_DrivesRunEViaContext(t *testing.T) {
	// Exercises the RunE happy path (config load → runServer) with a cancellable
	// context so the blocking server drains instead of hanging the test.
	t.Setenv("HTTP_PORT", strconv.Itoa(freePort(t)))
	ctx, cancel := context.WithCancel(context.Background())
	root := newRootCmd()
	root.SetArgs([]string{"server"})

	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("server command did not return after cancel")
	}
}

func TestHealthcheckCommand_ConnRefused(t *testing.T) {
	port := closedPort(t)
	t.Setenv("HTTP_PORT", strconv.Itoa(port))
	_, err := runRoot(t, "healthcheck")
	require.Error(t, err, "probe against a closed port must fail")
}

func TestHealthcheckCommand_InvalidConfigReturnsError(t *testing.T) {
	t.Setenv("HTTP_PORT", "not-a-port")
	_, err := runRoot(t, "healthcheck")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP_PORT")
}

// closedPort binds an ephemeral port, then closes it so a probe is refused.
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

// freePort returns a currently-free TCP port for tests that need a real bind.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func TestHealthcheck(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"200 ok", http.StatusOK, false},
		{"500 error", http.StatusInternalServerError, true},
		{"404 error", http.StatusNotFound, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/healthz", r.URL.Path)
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(srv.Close)

			err := checkHealth(context.Background(), srv.URL+"/healthz", healthClient())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHealthcheck_ConnectionRefused(t *testing.T) {
	// Bind then immediately close to obtain a definitely-closed port.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL + "/healthz"
	srv.Close()

	err := checkHealth(context.Background(), url, healthClient())
	assert.Error(t, err, "connection refused must be an error (non-zero exit)")
}

func TestHealthcheck_TimeoutBounded(t *testing.T) {
	// A server that never responds; the 2s client timeout must bound the call.
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-block
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	client := &http.Client{Timeout: 100 * time.Millisecond}
	err := checkHealth(context.Background(), srv.URL+"/healthz", client)
	assert.Error(t, err)
}

func validConfig() config.Config {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	return cfg
}
