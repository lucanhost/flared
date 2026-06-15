package flared

import (
	"context"
	"strings"
	"testing"
)

func TestValidateOptions_MissingOriginURL(t *testing.T) {
	err := validateOptions(Options{})
	if err == nil {
		t.Fatal("expected error for missing OriginURL")
	}
	if !strings.Contains(err.Error(), "OriginURL is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOptions_NameWithoutDomain(t *testing.T) {
	err := validateOptions(Options{
		OriginURL: "http://localhost:8080",
		Name:      "my-tunnel",
	})
	if err == nil {
		t.Fatal("expected error when Name is set without Domain")
	}
	if !strings.Contains(err.Error(), "both Name and Domain must be provided together") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOptions_DomainWithoutName(t *testing.T) {
	err := validateOptions(Options{
		OriginURL: "http://localhost:8080",
		Domain:    "example.com",
	})
	if err == nil {
		t.Fatal("expected error when Domain is set without Name")
	}
	if !strings.Contains(err.Error(), "both Name and Domain must be provided together") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOptions_ValidQuickTunnel(t *testing.T) {
	err := validateOptions(Options{OriginURL: "http://localhost:8080"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOptions_ValidNamedTunnel(t *testing.T) {
	err := validateOptions(Options{
		OriginURL: "http://localhost:8080",
		Name:      "my-tunnel",
		Domain:    "example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTunnel_URL(t *testing.T) {
	tun := &Tunnel{url: "https://abc.trycloudflare.com"}
	if got := tun.URL(); got != "https://abc.trycloudflare.com" {
		t.Fatalf("URL() = %q, want %q", got, "https://abc.trycloudflare.com")
	}
}

func TestTunnel_URL_Empty(t *testing.T) {
	tun := &Tunnel{}
	if got := tun.URL(); got != "" {
		t.Fatalf("URL() = %q, want empty", got)
	}
}

func TestTunnel_Close_Idempotent(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	tun := &Tunnel{
		cancel:   cancel,
		shutdown: make(chan struct{}),
	}
	tun.wg.Add(1)
	go func() {
		defer tun.wg.Done()
		<-tun.shutdown
	}()

	if err := tun.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := tun.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
}

func TestTunnel_Wait_NoError(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	tun := &Tunnel{
		cancel:   cancel,
		shutdown: make(chan struct{}),
	}
	tun.wg.Add(1)
	go func() {
		defer tun.wg.Done()
		<-tun.shutdown
	}()

	tun.Close()
	if err := tun.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, want nil", err)
	}
}

func TestTunnel_Wait_WithError(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	tun := &Tunnel{
		cancel:   cancel,
		shutdown: make(chan struct{}),
	}
	tun.wg.Add(1)
	go func() {
		defer tun.wg.Done()
		tun.err = context.Canceled
	}()

	if err := tun.Wait(); err != context.Canceled {
		t.Fatalf("Wait() error = %v, want %v", err, context.Canceled)
	}
}

func TestCertExists_NoCert(t *testing.T) {
	if certExists() {
		t.Skip("cert.pem exists on this system, skipping")
	}
}
