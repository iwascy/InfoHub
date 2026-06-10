package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"infohub/internal/agent"
	"infohub/internal/config"
)

const agentVersion = "0.1.0"

func main() {
	configPath := flag.String("config", defaultConfigPath(), "path to agent config file")
	once := flag.Bool("once", false, "run a single scan/push cycle and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("infohub-agent", agentVersion)
		return
	}

	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		slog.Error("load agent config failed", "error", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.Log.Level)
	runner := agent.NewRunner(cfg, agentVersion, logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *once {
		if err := runner.RunOnce(ctx); err != nil {
			logger.Error("agent run failed", "error", err)
			os.Exit(1)
		}
		return
	}

	logger.Info("infohub-agent started",
		"machine_id", cfg.MachineID,
		"server", cfg.Server.BaseURL,
		"interval", cfg.Interval().String(),
	)

	if err := runner.RunOnce(ctx); err != nil {
		logger.Warn("agent run failed", "error", err)
	}

	ticker := time.NewTicker(cfg.Interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("infohub-agent stopped")
			return
		case <-ticker.C:
			if err := runner.RunOnce(ctx); err != nil {
				logger.Warn("agent run failed", "error", err)
			}
		}
	}
}

func defaultConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "infohub-agent", "config.yaml")
	}
	return "config.yaml"
}

func newLogger(level string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: config.ParseLogLevel(level)}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}
