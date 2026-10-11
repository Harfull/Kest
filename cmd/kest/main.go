// Command kest runs the Kest personal assistant.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // embed zone data so time.LoadLocation works in minimal containers

	"github.com/Harfull/Kest/internal/app"
	"github.com/Harfull/Kest/internal/config"
	"github.com/Harfull/Kest/internal/logging"
	"github.com/Harfull/Kest/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "kest:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		configPath  = flag.String("config", "", "path to config.toml (default: <data dir>/config.toml)")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return nil
	}

	path := *configPath
	if path == "" {
		path = config.Path(os.LookupEnv)
	}

	cfg, err := config.Load(path, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("configuration:\n%w", err)
	}

	log, err := logging.New(os.Stderr, cfg.Log.Level, cfg.Log.Format)
	if err != nil {
		return err
	}

	a, err := app.New(cfg, log)
	if err != nil {
		return err
	}

	// SIGINT is what Pterodactyl sends for the egg's "^C" stop command.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return a.Run(ctx)
}
