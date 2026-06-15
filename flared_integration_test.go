//go:build integration

package flared

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// cloudflared's orchestrator, signal handlers, and Prometheus metrics are global
// process state. tunnel.Init() + Start() can only be called ONCE per process.
// Tests that call Start() must run in subprocess isolation.

const envSubprocess = "FLARED_INTEGRATION_SUB"

func runSubprocess(t *testing.T) {
	t.Helper()
	if os.Getenv(envSubprocess) == "1" {
		return
	}
	cmd := exec.Command("go", "test", "-v", "-tags=integration", "-run", "^"+t.Name()+"$", "-timeout", "30s", ".")
	cmd.Env = append(os.Environ(), envSubprocess+"=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("subprocess failed: %v", err)
	}
	t.Skip("ran in subprocess")
}

func TestIntegration_QuickTunnel(t *testing.T) {
	if os.Getenv(envSubprocess) != "1" {
		runSubprocess(t)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tun, err := Start(ctx, Options{
		OriginURL: "http://localhost:19999",
		Timeout:   15 * time.Second,
	})
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	defer tun.Close()

	url := tun.URL()
	if !strings.Contains(url, "trycloudflare.com") {
		t.Fatalf("expected trycloudflare.com URL, got %q", url)
	}
	t.Logf("Tunnel URL: %s", url)
}

func TestIntegration_QuickTunnel_Timeout(t *testing.T) {
	if os.Getenv(envSubprocess) != "1" {
		runSubprocess(t)
		return
	}

	start := time.Now()
	_, err := Start(context.Background(), Options{
		OriginURL: "http://localhost:19999",
		Timeout:   500 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestIntegration_ValidateOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr string
	}{
		{
			name:    "empty options",
			opts:    Options{},
			wantErr: "OriginURL is required",
		},
		{
			name:    "name without domain",
			opts:    Options{OriginURL: "http://localhost:8080", Name: "t"},
			wantErr: "both Name and Domain must be provided together",
		},
		{
			name:    "domain without name",
			opts:    Options{OriginURL: "http://localhost:8080", Domain: "d.com"},
			wantErr: "both Name and Domain must be provided together",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOptions(tt.opts)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}
