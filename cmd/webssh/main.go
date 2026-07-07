// Command webssh runs a local web control panel for managing SSH connections.
//
// It binds to loopback only and prints a tokenized URL to open in the browser.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	webassets "webssh/web"

	"webssh/internal/config"
	"webssh/internal/health"
	"webssh/internal/launcher"
	"webssh/internal/server"
	"webssh/internal/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8022", "listen address (must be loopback)")
	noOpen := flag.Bool("no-open", false, "do not open the browser automatically")
	flag.Parse()

	if err := run(*addr, *noOpen); err != nil {
		log.Fatalf("webssh: %v", err)
	}
}

func run(addr string, noOpen bool) error {
	if err := ensureLoopback(addr); err != nil {
		return err
	}

	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}

	st, err := store.Open(paths.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	token, err := loadOrCreateAPIKey(paths.DataDir)
	if err != nil {
		return err
	}

	// Background reachability checks feed the inventory status dots.
	hcCtx, hcCancel := context.WithCancel(context.Background())
	defer hcCancel()
	hc := health.New(st)
	go hc.Run(hcCtx)

	srv := server.New(st, paths, token, webassets.FS(), hc)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/?token=%s", ln.Addr().String(), token)
	fmt.Printf("webssh listening — open:\n\n    %s\n\n", url)

	httpSrv := &http.Server{Handler: srv.Handler()}

	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("serve: %v", err)
		}
	}()

	if !noOpen {
		openBrowser(url, st.GetSetting(config.KeyBrowserCmd, ""))
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	fmt.Println("\nshutting down…")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpSrv.Shutdown(ctx)
}

// ensureLoopback refuses to bind to a non-loopback interface — this daemon runs
// with the user's privileges and must never be exposed to the network.
func ensureLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid --addr %q: %w", addr, err)
	}
	if host == "" || host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("--addr host %q is not loopback; refusing to expose webssh to the network", host)
	}
	return nil
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// loadOrCreateAPIKey returns a stable API key for authenticating the SPA to the
// API, generating and persisting one (mode 0600) on first run. Unlike a random
// per-start token, this keeps the launch URL constant across restarts.
func loadOrCreateAPIKey(dataDir string) (string, error) {
	path := filepath.Join(dataDir, "apikey")
	if b, err := os.ReadFile(path); err == nil {
		if k := strings.TrimSpace(string(b)); k != "" {
			return k, nil
		}
	}
	k, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(k), 0o600); err != nil {
		return "", err
	}
	return k, nil
}

// openBrowser opens rawURL. When browserCmd is set it is used (with {{url}}
// substituted, or the URL appended when the placeholder is absent); otherwise
// the platform default handler is used.
func openBrowser(rawURL, browserCmd string) {
	var name string
	var args []string

	if strings.TrimSpace(browserCmd) != "" {
		hasPlaceholder := strings.Contains(browserCmd, "{{url}}")
		tmpl := strings.ReplaceAll(browserCmd, "{{url}}", rawURL)
		if fields, err := launcher.SplitFields(tmpl); err != nil || len(fields) == 0 {
			log.Printf("invalid browser command %q (%v); using system default", browserCmd, err)
		} else {
			name, args = fields[0], fields[1:]
			if !hasPlaceholder {
				args = append(args, rawURL)
			}
		}
	}

	if name == "" {
		switch runtime.GOOS {
		case "darwin":
			name = "open"
		case "windows":
			name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
		default:
			name = "xdg-open"
		}
		args = append(args, rawURL)
	}

	if err := exec.Command(name, args...).Start(); err != nil {
		log.Printf("could not open browser (%v); open the URL above manually", err)
	}
}
