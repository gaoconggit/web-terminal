package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"web-terminal/internal/webterm"
)

func main() {
	cfg, err := webterm.LoadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	srv, err := webterm.NewServer(cfg)
	if err != nil {
		log.Fatalf("init server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), webterm.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, webterm.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}
