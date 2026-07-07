package main

import (
	"log"
	"net/http"
	"os"

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

	dbPath := os.Getenv("GUDA_DB_PATH")
	if dbPath == "" {
		dbPath = "guda-gateway.db"
	}
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	mkPath := os.Getenv("GUDA_MASTER_KEY_PATH")
	if mkPath == "" {
		mkPath = "master.key"
	}
	mk, err := secrets.LoadOrCreate(mkPath)
	if err != nil {
		log.Fatal(err)
	}

	gk := gatewaykeys.NewService(st.DB())
	log.Printf("guda gateway listening on %s", cfg.Addr)
	if err := http.ListenAndServe(cfg.Addr, server.New(cfg, gk, st.DB(), mk)); err != nil {
		log.Fatal(err)
	}
}