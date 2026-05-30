package flared

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/cloudflare/cloudflared/cmd/cloudflared/cliutil"
	"github.com/cloudflare/cloudflared/cmd/cloudflared/tunnel"
	"github.com/urfave/cli/v2"
)

// Options contains the configuration for starting a cloudflared tunnel.
type Options struct {
	// Name is the stable name to identify the tunnel. Used along with Domain to create, route, and run a tunnel.
	// Requires cloudflare credentials (cert.pem) to be present.
	Name string
	// Domain is the expected public hostname for Named Tunnels (e.g., "app.example.com").
	// If provided alongside Name, it will be used to route traffic.
	Domain string
	// OriginURL is the local service URL to expose (e.g. "http://127.0.0.1:8080").
	OriginURL string
	// ShowLog determines whether cloudflared's internal logs should be printed to os.Stdout.
	ShowLog bool
}

// Tunnel represents an active, in-process Cloudflare Tunnel.
type Tunnel struct {
	url       string
	cancel    context.CancelFunc
	shutdown  chan struct{}
	errCh     chan error
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// URL returns the public URL where the tunnel is accessible.
// For Quick Tunnels, this is the generated trycloudflare.com address.
// For Named Tunnels, this is the Domain provided in Options.
func (t *Tunnel) URL() string {
	return t.url
}

// Close gracefully shuts down the tunnel and stops the internal cloudflared process.
func (t *Tunnel) Close() error {
	t.closeOnce.Do(func() {
		// cloudflared might close the shutdown channel itself on SIGINT, 
		// so we recover from any "close of closed channel" panics here.
		defer func() { recover() }()
		close(t.shutdown)
		t.cancel()
	})
	t.wg.Wait()
	return nil
}

// Wait blocks until the tunnel is closed or encounters a fatal error.
func (t *Tunnel) Wait() error {
	t.wg.Wait()
	select {
	case err := <-t.errCh:
		return err
	default:
		return nil
	}
}

func certExists() bool {
	u, err := user.Current()
	if err != nil {
		return false
	}
	certPath := filepath.Join(u.HomeDir, ".cloudflared", "cert.pem")
	fileInfo, err := os.Stat(certPath)
	if err == nil && fileInfo.Size() > 0 {
		return true
	}
	return false
}

// Start creates and runs a tunnel in-process. It blocks until the tunnel is established.
func Start(ctx context.Context, opts Options) (*Tunnel, error) {
	if opts.OriginURL == "" {
		return nil, fmt.Errorf("OriginURL is required")
	}

	if (opts.Name != "" && opts.Domain == "") || (opts.Name == "" && opts.Domain != "") {
		return nil, fmt.Errorf("both Name and Domain must be provided together for Named Tunnels")
	}

	if opts.Name != "" && !certExists() {
		fmt.Println("cert.pem not found. Starting Cloudflare login process...")
		loginApp := &cli.App{
			Name:     "cloudflared",
			Commands: tunnel.Commands(),
		}
		if err := loginApp.RunContext(ctx, []string{"cloudflared", "tunnel", "login"}); err != nil {
			return nil, fmt.Errorf("failed to login: %w", err)
		}
	}

	tCtx, cancel := context.WithCancel(ctx)
	t := &Tunnel{
		cancel:   cancel,
		shutdown: make(chan struct{}),
		errCh:    make(chan error, 1),
	}

	// Prepare arguments for the cli App
	args := []string{"cloudflared", "tunnel"}
	if opts.Name != "" && opts.Domain != "" {
		// Note: We pass --output "" to workaround an upstream bug in cloudflared 
		// where the global output flag defaults to 'default' but create() expects '' or 'json'/'yaml'.
		args = append(args, "--output", "", "--name", opts.Name, "--hostname", opts.Domain, "--overwrite-dns", "--url", opts.OriginURL)
	} else {
		args = append(args, "--url", opts.OriginURL)
	}

	var oldStderr *os.File
	var pw *os.File
	urlCh := make(chan string, 1)

	if opts.Name == "" {
		// Quick Tunnel: Must intercept os.Stderr to find the URL
		pr, pipeWriter, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create pipe: %w", err)
		}
		pw = pipeWriter
		oldStderr = os.Stderr
		os.Stderr = pw

		go func() {
			scanner := bufio.NewScanner(pr)
			urlRegex := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)
			for scanner.Scan() {
				line := scanner.Text()
				if opts.ShowLog {
					fmt.Fprintln(oldStderr, line)
				}
				if match := urlRegex.FindString(line); match != "" {
					select {
					case urlCh <- match:
					default:
					}
				}
			}
		}()
	} else if !opts.ShowLog {
		// Named Tunnel + Hide Logs: Just redirect to /dev/null
		devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0666)
		if err == nil {
			oldStderr = os.Stderr
			pw = devNull
			os.Stderr = pw
		}
	}

	bInfo := cliutil.GetBuildInfo("DEV", "unknown")
	tunnel.Init(bInfo, t.shutdown)

	app := &cli.App{
		Name:     "cloudflared",
		Commands: tunnel.Commands(),
	}

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer func() {
			if oldStderr != nil {
				os.Stderr = oldStderr
			}
			if pw != nil {
				pw.Close()
			}
		}()
		err := app.RunContext(tCtx, args)
		if err != nil {
			t.errCh <- err
		}
	}()

	if opts.Name == "" {
		// Wait for the Quick Tunnel URL to be printed
		select {
		case url := <-urlCh:
			t.url = url
		case <-time.After(15 * time.Second):
			t.Close()
			return nil, fmt.Errorf("timeout waiting for quick tunnel URL")
		case err := <-t.errCh:
			t.Close()
			return nil, fmt.Errorf("tunnel failed to start: %w", err)
		}
	} else {
		if opts.Domain != "" {
			domain := opts.Domain
			// Ensure it has a protocol scheme if it doesn't already
			if len(domain) > 0 && domain[:4] != "http" {
				domain = "https://" + domain
			}
			t.url = domain
		}
		// Give Named Tunnel a short moment to initialize before returning
		// (Named Tunnels do not print a URL)
		time.Sleep(2 * time.Second)
	}

	return t, nil
}
