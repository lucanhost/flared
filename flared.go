package flared

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cloudflare/cloudflared/cmd/cloudflared/cliutil"
	"github.com/cloudflare/cloudflared/cmd/cloudflared/tunnel"
	"github.com/urfave/cli/v2"
)

var stderrMu sync.Mutex

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
	// ShowLog determines whether cloudflared's internal logs should be printed to os.Stderr.
	ShowLog bool
	// LogWriter is an optional writer to receive cloudflared's internal logs.
	// If nil and ShowLog is false, logs are suppressed.
	LogWriter io.Writer
	// Timeout is the maximum time to wait for a Quick Tunnel URL.
	// Defaults to 15 seconds if zero.
	Timeout time.Duration
}

// Tunnel represents an active, in-process Cloudflare Tunnel.
type Tunnel struct {
	url       string
	cancel    context.CancelFunc
	shutdown  chan struct{}
	err       error
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
		t.cancel()
		// cloudflared might close the shutdown channel itself on SIGINT,
		// so we use recover to handle "close of closed channel" panics.
		defer func() {
			if r := recover(); r != nil {
				// Channel already closed by cloudflared, safe to ignore.
			}
		}()
		close(t.shutdown)
	})
	t.wg.Wait()
	return nil
}

// Wait blocks until the tunnel is closed or encounters a fatal error.
func (t *Tunnel) Wait() error {
	t.wg.Wait()
	return t.err
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

// validateOptions checks that the given options are valid before starting a tunnel.
func validateOptions(opts Options) error {
	if opts.OriginURL == "" {
		return fmt.Errorf("OriginURL is required")
	}
	if (opts.Name != "" && opts.Domain == "") || (opts.Name == "" && opts.Domain != "") {
		return fmt.Errorf("both Name and Domain must be provided together for Named Tunnels")
	}
	return nil
}

// Start creates and runs a tunnel in-process. It blocks until the tunnel is established.
func Start(ctx context.Context, opts Options) (*Tunnel, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
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

		stderrMu.Lock()
		oldStderr = os.Stderr
		os.Stderr = pw
		stderrMu.Unlock()

		logWriter := io.Discard
		if opts.ShowLog {
			if opts.LogWriter != nil {
				logWriter = opts.LogWriter
			} else {
				logWriter = oldStderr
			}
		}

		go func() {
			scanner := bufio.NewScanner(pr)
			urlRegex := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)
			for scanner.Scan() {
				line := scanner.Text()
				fmt.Fprintln(logWriter, line)
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
			stderrMu.Lock()
			oldStderr = os.Stderr
			pw = devNull
			os.Stderr = pw
			stderrMu.Unlock()
		}
	}

	bInfo := cliutil.GetBuildInfo("DEV", "unknown")
	tunnel.Init(bInfo, t.shutdown)

	app := &cli.App{
		Name:            "cloudflared",
		Commands:        tunnel.Commands(),
		ExitErrHandler:  func(c *cli.Context, err error) {},
	}

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer func() {
			if oldStderr != nil {
				stderrMu.Lock()
				os.Stderr = oldStderr
				stderrMu.Unlock()
			}
			if pw != nil {
				pw.Close()
			}
		}()
		err := app.RunContext(tCtx, args)
		// Ignore errors caused by context cancellation (graceful shutdown)
		if err != nil && tCtx.Err() == nil {
			t.err = err
		}
	}()

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}

	if opts.Name == "" {
		// Wait for the Quick Tunnel URL to be printed
		select {
		case url := <-urlCh:
			t.url = url
		case <-time.After(timeout):
			t.Close()
			return nil, fmt.Errorf("timeout waiting for quick tunnel URL")
		case <-tCtx.Done():
			return nil, fmt.Errorf("tunnel context cancelled")
		}
	} else {
		if opts.Domain != "" {
			domain := opts.Domain
			// Ensure it has a protocol scheme if it doesn't already
			if !strings.HasPrefix(domain, "http") {
				domain = "https://" + domain
			}
			t.url = domain
		}
	}

	return t, nil
}
