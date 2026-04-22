package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"mc-proxy/internal/config"
	"mc-proxy/internal/proxy"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	showVersion := flag.Bool("version", false, "Print build version")
	flag.Parse()

	if *showVersion {
		fmt.Println(versionString())
		return
	}

	logger := log.New(os.Stdout, "[mc-proxy] ", log.LstdFlags|log.Lmicroseconds|log.LUTC)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Printf("load config failed: %v", err)
		os.Exit(1)
	}

	mgr, err := proxy.NewManager(*cfg, logger)
	if err != nil {
		logger.Printf("create manager failed: %v", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := mgr.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Printf("runtime error: %v", err)
		os.Exit(1)
	}
}

func versionString() string {
	return fmt.Sprintf("mc-proxy version=%s commit=%s buildTime=%s", version, commit, buildTime)
}
