package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/chuansuoyun/socratex/internal/client"
	"github.com/chuansuoyun/socratex/internal/config"
	"github.com/chuansuoyun/socratex/internal/server"
)

// version is set at build time via: -ldflags "-X main.version=v2.0.0"
var version = "2.0.0"

func main() {
	configPath := flag.String("config", "config.json", "path to config.json")
	genExample := flag.Bool("example", false, "write config.example.json and exit")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("socratex %s\n", version)
		return
	}
	if *genExample {
		path := "config.example.json"
		if err := config.SaveExample(path); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote %s\n", path)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v\n\nTip: run  socratex -example  to generate config.example.json", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("socratex %s  mode=%s", version, cfg.Mode)

	switch cfg.Mode {
	case "server":
		srv, err := server.New(cfg.Server)
		if err != nil {
			log.Fatal(err)
		}
		if err := srv.Run(ctx); err != nil {
			log.Fatal(err)
		}
	case "client":
		cli, err := client.New(cfg.Client)
		if err != nil {
			log.Fatal(err)
		}
		if err := cli.Run(ctx); err != nil {
			log.Fatal(err)
		}
	}
}
