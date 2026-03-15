package service

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/p0rtal-4/p0rtal/internal/api"
	"github.com/p0rtal-4/p0rtal/internal/config"
	"github.com/p0rtal-4/p0rtal/internal/credentials"
	"github.com/p0rtal-4/p0rtal/internal/session"
)

// Agent holds all initialised components of the gateway agent. It is returned
// by StartAgent and provides a Shutdown method for graceful teardown.
type Agent struct {
	cfg        *config.Config
	credStore  *credentials.Store
	mgr        *session.Manager
	httpServer *http.Server
}

// StartAgent loads configuration, initialises every subsystem, and starts
// the HTTP server in a background goroutine. The returned Agent can be shut
// down later by calling its Shutdown method.
func StartAgent(configPath string) (*Agent, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}

	// Set up logging: always write to stdout; additionally write to a log
	// file when configured.
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, err
		}
		log.SetOutput(io.MultiWriter(os.Stdout, f))
	} else {
		log.SetOutput(os.Stdout)
	}

	credStore, err := credentials.New(cfg)
	if err != nil {
		return nil, err
	}

	mgr, err := session.NewManager(cfg, credStore)
	if err != nil {
		credStore.Close()
		return nil, err
	}

	router := api.NewRouter(mgr, credStore, cfg)

	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: router,
	}

	go func() {
		log.Printf("Gateway Agent listening on %s", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	return &Agent{
		cfg:        cfg,
		credStore:  credStore,
		mgr:        mgr,
		httpServer: httpServer,
	}, nil
}

// Shutdown gracefully stops all agent components in the correct order:
// HTTP server first, then session manager, then credential store.
func (a *Agent) Shutdown() {
	log.Println("Gateway Agent shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := a.httpServer.Shutdown(ctx); err != nil {
		log.Printf("http server shutdown error: %v", err)
	}

	a.mgr.Shutdown()
	a.credStore.Close()

	log.Println("Gateway Agent stopped")
}
