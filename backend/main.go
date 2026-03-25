package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/otiv/backend/internal/api"
	"github.com/otiv/backend/internal/config"
	"github.com/otiv/backend/internal/vpn"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	manager, err := vpn.NewManager(cfg)
	if err != nil {
		log.Fatalf("init manager: %v", err)
	}

	handler := api.NewHandler(manager, cfg.FrontendURL)

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler.Routes(),
	}

	go func() {
		if cfg.TLS.Enabled() {
			log.Printf("listening on %s (HTTPS)", cfg.ListenAddr)
			err = srv.ListenAndServeTLS(cfg.TLS.Cert, cfg.TLS.Key)
		} else {
			log.Printf("listening on %s (HTTP)", cfg.ListenAddr)
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	s := <-quit
	log.Printf("signal %s — shutting down", s)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)

	log.Printf("stopping all vpn instances...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()
	manager.Shutdown(shutCtx)
	log.Printf("done")
}
