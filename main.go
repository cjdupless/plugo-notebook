package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/cjdupless/plugo-notebook/internal/notebook"
	"github.com/cjdupless/plugo-notebook/internal/runner"
	"github.com/cjdupless/plugo-notebook/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	dir := flag.String("dir", "./notebooks", "Directory to store .plugo notebooks")
	openBrowser := flag.Bool("open", true, "Automatically open Plugo in your default browser")
	timeout := flag.Duration("timeout", 15*time.Second, "Timeout per Go cell execution")
	flag.Parse()

	store, err := notebook.NewStore(*dir)
	if err != nil {
		log.Fatalf("plugo: failed to open notebook directory: %v", err)
	}
	if _, err := store.EnsureStarterNotebook(); err != nil {
		log.Fatalf("plugo: failed to initialize starter notebook: %v", err)
	}

	executor := runner.NewExecutor(*timeout, store.Dir())
	srvHandler, err := server.New(store, executor)
	if err != nil {
		log.Fatalf("plugo: failed to initialize server: %v", err)
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("plugo: failed to listen on %s: %v", *addr, err)
	}

	url := fmt.Sprintf("http://%s", ln.Addr().String())
	httpServer := &http.Server{
		Handler:           srvHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Println()
	fmt.Println("  Plugo Notebook (Go-Native)")
	fmt.Printf("  -> Local:      %s\n", url)
	fmt.Printf("  -> Notebooks:  %s\n", store.Dir())
	fmt.Printf("  -> Toolchain:  %s\n", executor.GoVersion())
	fmt.Println("  Press Ctrl+C to stop.")
	fmt.Println()

	if *openBrowser {
		go func() {
			time.Sleep(250 * time.Millisecond)
			launchBrowser(url)
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatalf("plugo: server error: %v", err)
		}
	case <-sigCh:
		fmt.Println("\nShutting down Plugo...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}
}

func launchBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
