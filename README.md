# flared

`flared` is a Go library that allows you to run Cloudflare Tunnels (`cloudflared`) directly from your Go code.

It supports both **Quick Tunnels** (temporary tunnels via `trycloudflare.com`) and **Named Tunnels** (custom domains via Cloudflare DNS).

## Installation

```bash
go get github.com/lucanhost/flared
```

## Quick Start

### Quick Tunnel (trycloudflare.com)
Creates a temporary, random URL to expose your local service.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/lucanhost/flared"
)

func main() {
	// 1. Start a simple HTTP server
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Welcome to the Go HTTP Server (Quick Tunnel)!")
	})
	go func() {
		if err := http.ListenAndServe(":8080", nil); err != nil {
			log.Fatal(err)
		}
	}()

	// 2. Start Cloudflare Tunnel
	opts := flared.Options{
		OriginURL: "http://localhost:8080", // Local URL to expose
		ShowLog:   false,                   // Toggle cloudflared internal logs
	}

	tunnel, err := flared.Start(context.Background(), opts)
	if err != nil {
		log.Fatal(err)
	}
	defer tunnel.Close()

	fmt.Printf("Tunnel URL: %s\n", tunnel.URL())
	tunnel.Wait()
}
```

### Named Tunnel (Custom Domain)
Creates and routes a stable tunnel to your custom domain. Requires a valid `cert.pem` on the host. If the certificate is missing, the library will automatically trigger the `cloudflared` login flow via your terminal.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/lucanhost/flared"
)

func main() {
	// 1. Start a simple HTTP server
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Welcome to the Go HTTP Server (Named Tunnel)!")
	})
	go func() {
		if err := http.ListenAndServe(":8080", nil); err != nil {
			log.Fatal(err)
		}
	}()

	// 2. Start Cloudflare Tunnel
	opts := flared.Options{
		OriginURL: "http://localhost:8080",
		Name:      "my-demo-tunnel",
		Domain:    "api.example.com", 
		ShowLog:   false,
	}

	tunnel, err := flared.Start(context.Background(), opts)
	if err != nil {
		log.Fatal(err)
	}
	defer tunnel.Close()

	fmt.Printf("Tunnel URL: %s\n", tunnel.URL())
	tunnel.Wait()
}
```

## How it works

Under the hood, `flared` imports the official `github.com/cloudflare/cloudflared` module and invokes its internal CLI commands directly in your process. It intercepts standard outputs using an in-memory pipe (`os.Pipe`) to parse necessary information (like the Quick Tunnel URL) securely, with zero disk I/O.

> [!WARNING]
> **Binary Size Impact**
> Because `flared` statically compiles the entire `cloudflared` core into your application, your final compiled binary size will increase by approximately **30-40 MB**. This is the expected trade-off for achieving a seamless, zero-dependency deployment for your users.

## Configuration (Options)

- `OriginURL` (string): The local service URL to expose (e.g., `http://localhost:8080`). **(Required)**
- `Name` (string): The stable name of the tunnel. Used alongside `Domain` to create and route a Named Tunnel.
- `Domain` (string): The public hostname to route traffic through (e.g., `api.example.com`).
- `ShowLog` (bool): Toggles the output of internal `cloudflared` logs to `os.Stderr`.

*Note: If both `Name` and `Domain` are omitted, the library defaults to creating a Quick Tunnel.*
