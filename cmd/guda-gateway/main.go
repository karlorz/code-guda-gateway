package main

import (
	"log"
	"net/http"

	"code-guda-gateway/internal/config"
	"code-guda-gateway/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("guda gateway listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, server.New(cfg)); err != nil {
		log.Fatal(err)
	}
}
