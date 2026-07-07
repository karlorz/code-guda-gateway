package main

import (
	"log"
	"net/http"

	"code-guda-gateway/internal/config"
	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/server"
	"code-guda-gateway/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	mk, err := secrets.LoadOrCreate(cfg.MasterKeyPath)
	if err != nil {
		log.Fatal(err)
	}

	gk := gatewaykeys.NewService(st.DB())
	log.Printf("guda gateway listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, server.New(cfg, gk, st.DB(), mk)); err != nil {
		log.Fatal(err)
	}
}
