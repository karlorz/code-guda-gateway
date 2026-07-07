package main

import (
	"flag"
	"os"
)

const (
	defaultDBPath     = "/var/lib/code-guda-gateway/gateway.db"
	defaultMasterPath = "/etc/code-guda-gateway/master.key"
)

func main() {
	fs := flag.NewFlagSet("guda-gateway-admin", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dbPath := fs.String("db", defaultDBPath, "SQLite database path")
	masterPath := fs.String("master-key", defaultMasterPath, "master encryption key file")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(exitUsage)
	}
	code := runWithIO(*dbPath, *masterPath, fs.Args(), os.Stdout, os.Stderr, os.Stdin)
	os.Exit(code)
}
