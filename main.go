package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"r2/internal/config"
	"r2/internal/r2"
	"r2/internal/server"
)

func main() {
	// Load .env from the current working directory if present. The `r2` wrapper
	// cd's into the repo before launching, so this finds the repo's .env.
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("note: could not load .env (%v) — relying on existing environment", err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()
	client, err := r2.New(ctx, cfg)
	if err != nil {
		log.Fatalf("r2 client: %v", err)
	}

	srv := server.New(client)
	addr := "127.0.0.1:" + cfg.Port
	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler()}

	fmt.Println()
	fmt.Printf("  R2 file manager running at  http://%s\n", addr)
	if !cfg.HasCFToken() {
		fmt.Println("  (no CF_API_TOKEN set — links will be presigned/expiring only)")
	}
	fmt.Println()

	// Graceful shutdown on SIGTERM/SIGINT (used by `r2 die`).
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}
