package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"mcgo/internal/server"
)

func main() {
	cfg := server.DefaultConfig()
	flag.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	flag.StringVar(&cfg.MOTD, "motd", cfg.MOTD, "server list description")
	flag.IntVar(&cfg.MaxPlayers, "max", cfg.MaxPlayers, "advertised max players")
	flag.StringVar(&cfg.WorldPath, "world", cfg.WorldPath, "world edit log path")
	flag.StringVar(&cfg.PlayersPath, "players", cfg.PlayersPath, "player state path")
	flag.StringVar(&cfg.SignsPath, "signs", cfg.SignsPath, "sign text path")
	flag.StringVar(&cfg.ContainersPath, "containers", cfg.ContainersPath, "container contents path")
	flag.StringVar(&cfg.RegionDir, "region", cfg.RegionDir, "saved world directory containing region/ (empty for the flat world)")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	srv := server.New(cfg)

	// Flush world edits on SIGINT/SIGTERM so a restart keeps the changes.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		srv.Shutdown()
		os.Exit(0)
	}()

	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
