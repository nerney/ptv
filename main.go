package main

import (
	"context"
	"embed"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nerney/ptv/internal/config"
	"github.com/nerney/ptv/internal/defs"
	"github.com/nerney/ptv/internal/handlers"
	"github.com/nerney/ptv/internal/logger"
	_ "github.com/nerney/ptv/internal/unit3d" // registers UNIT3D TrackerType
)

//go:embed templates static
var assets embed.FS

// banner is the ASCII-art PTV mark printed at startup. Kept as a raw
// string literal so the box-drawing glyphs survive any indent rewriting.
const banner = `
██████╗ ████████╗██╗   ██╗
██╔══██╗╚══██╔══╝██║   ██║
██████╔╝   ██║   ╚██╗ ██╔╝
██╔═══╝    ██║    ╚████╔╝
╚═╝        ╚═╝     ╚═══╝
`

func main() {
	configDir := os.Getenv("CONFIG_DIR")
	if configDir == "" {
		configDir = "/config"
	}

	log := logger.New()
	log.Banner(banner)

	store, err := config.NewStore(configDir)
	if err != nil {
		log.Err("SYSTEM", "config store: "+err.Error())
		os.Exit(1)
	}

	syncer := defs.New(configDir, log)
	syncer.Start(context.Background())

	router := handlers.NewRouter(store, syncer, assets)

	srv := &http.Server{
		Addr:         "[::]:8008",
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("SYSTEM", "ptv listening on "+srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Err("SYSTEM", "server: "+err.Error())
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Err("SYSTEM", "shutdown: "+err.Error())
	}
}
