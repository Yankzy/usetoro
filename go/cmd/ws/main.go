package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yankzy/usetoro/internal/wshandler"
)

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Get configuration from environment
	addr := os.Getenv("WS_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	// QBO Configuration
	qboConfig := &wshandler.Config{
		QBOClientID:     os.Getenv("QBO_CLIENT_ID"),
		QBOClientSecret: os.Getenv("QBO_CLIENT_SECRET"),
		QBORedirectURI:  os.Getenv("QBO_REDIRECT_URI"),
		QBOIsProduction: os.Getenv("QBO_IS_PRODUCTION") == "true",
	}

	// Default redirect URI if not set
	if qboConfig.QBORedirectURI == "" {
		qboConfig.QBORedirectURI = "http://localhost/api/auth/qbo/callback"
	}

	logger.Info("Starting WebSocket server",
		"addr", addr,
		"qbo_configured", qboConfig.QBOClientID != "",
	)

	// Create WebSocket hub
	hub := wshandler.NewHub(logger)
	go hub.Run()

	// Create message handler
	messageHandler := wshandler.NewMessageHandler(logger, qboConfig)

	// Create WebSocket handler
	wsHandler := wshandler.NewHandler(hub, logger, messageHandler)

	// Setup HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsHandler.ServeWS)

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","clients":` + string(rune(hub.ClientCount())) + `}`))
	})

	// Create HTTP server
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("WebSocket server listening", "addr", addr)
		serverErrors <- server.ListenAndServe()
	}()

	// Setup graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	// Wait for either error or shutdown signal
	select {
	case err := <-serverErrors:
		logger.Error("Server error", "error", err)
		os.Exit(1)

	case sig := <-shutdown:
		logger.Info("Shutdown signal received", "signal", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("Could not stop server gracefully", "error", err)
			server.Close()
			os.Exit(1)
		}

		logger.Info("Server stopped gracefully")
	}
}
