package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/daemon"
)

func main() {
	flag.Parse()

	// Structured Logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Pass a load functor so the TAP daemon can fetch our configuration
	loader := config.Load

	// Spin up generic TAP Daemon
	d := daemon.New(logger, loader, ":9090")

	if err := d.Run(context.Background()); err != nil {
		logger.Error("Protocol Daemon crashed", "error", err)
		os.Exit(1)
	}
}
