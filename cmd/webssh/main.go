// Command webssh runs a local web control panel for managing SSH connections.
//
// It binds to loopback only and prints a tokenized URL to open in the browser.
package main

import (
	"cmp"
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
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	webassets "webssh/web"

	"webssh/internal/appwin"
	"webssh/internal/config"
	"webssh/internal/health"
	"webssh/internal/server"
	"webssh/internal/store"
	"webssh/internal/update"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8022", "listen address (must be loopback)")
	noOpen := flag.Bool("no-open", false, "do not open the interface automatically")
	ui := flag.String("ui", "", "how to open the interface: browser|app|webview (default: the ui_mode setting)")
	setUI := flag.String("set-ui-mode", "", "store browser|app|webview as the default and exit (used by install.sh)")
	setAppBrowser := flag.String("set-app-browser", "", "store which browser the app window uses and exit (used by install.sh)")
	listBrowsers := flag.Bool("list-app-browsers", false, "print the chromium-based browsers that can host an app window, and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		// update.Current, not version.Version: a binary built with a bare
		// `go build` carries no stamp, and answering "dev" when the working
		// copy can say exactly which commit it is would be needlessly unhelpful.
		fmt.Println("webssh", update.Current())
		return
	}

	if *listBrowsers {
		for _, b := range appwin.FindChromiumAll() {
			fmt.Println(b)
		}
		return
	}

	if *setUI != "" || *setAppBrowser != "" {
		if err := storeSettings(*setUI, *setAppBrowser); err != nil {
			log.Fatalf("webssh: %v", err)
		}
		return
	}

	if *ui != "" && !appwin.Valid(*ui) {
		log.Fatalf("webssh: --ui %q is not one of browser|app|webview", *ui)
	}

	// GTK requires its window to live on the process's main thread, so the
	// webview mode needs main's goroutine pinned. Harmless for the others.
	runtime.LockOSThread()

	if err := run(*addr, *noOpen, *ui); err != nil {
		log.Fatalf("webssh: %v", err)
	}
}

// storeSettings persists the interface preferences without starting the server,
// so the installer can record the user's choices in the same place the Settings
// panel writes them. Either argument may be empty, meaning "leave alone".
func storeSettings(mode, appBrowser string) error {
	if mode != "" && !appwin.Valid(mode) {
		return fmt.Errorf("--set-ui-mode %q is not one of browser|app|webview", mode)
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

	if mode != "" {
		if err := st.SetSetting(config.KeyUIMode, mode); err != nil {
			return err
		}
		fmt.Printf("ui_mode = %s\n", mode)
	}
	if appBrowser != "" {
		// "auto" is how the installer says "go back to detecting one".
		if appBrowser == "auto" {
			appBrowser = ""
		}
		if err := st.SetSetting(config.KeyAppBrowser, appBrowser); err != nil {
			return err
		}
		fmt.Printf("app_browser = %s\n", cmp.Or(appBrowser, "(auto-detect)"))
	}
	return nil
}

func run(addr string, noOpen bool, uiFlag string) error {
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

	// Shutdown is requested by SIGINT/SIGTERM or by the last tab closing (see
	// internal/server/presence.go).
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	waitForStop := func() {
		select {
		case <-stop:
		case <-srv.Quit():
		}
	}

	if noOpen {
		waitForStop()
	} else {
		mode := appwin.ParseMode(st.GetSetting(config.KeyUIMode, config.Defaults(paths.Home)[config.KeyUIMode]))
		if uiFlag != "" {
			mode = appwin.ParseMode(uiFlag)
		}
		win := appwin.New(mode, paths,
			st.GetSetting(config.KeyBrowserCmd, ""),
			st.GetSetting(config.KeyAppBrowser, ""))

		if win.Blocking() {
			// A webview owns this goroutine until its window closes, so the
			// shutdown signal has to arrive from the side and close it.
			go func() { waitForStop(); win.Close() }()
			if err := win.Open(url); err != nil {
				log.Printf("could not open the window (%v); open the URL above manually", err)
				waitForStop()
			}
		} else {
			if err := win.Open(url); err != nil {
				log.Printf("could not open the interface (%v); open the URL above manually", err)
			}
			waitForStop()
		}
	}

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
