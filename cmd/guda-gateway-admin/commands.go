package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"code-guda-gateway/internal/adminauth"
	"code-guda-gateway/internal/audit"
	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/store"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

type app struct {
	dbPath     string
	masterPath string
	stdout     io.Writer
	stderr     io.Writer
	stdin      io.Reader
}

func runWithIO(dbPath, masterPath string, args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	a := &app{
		dbPath:     dbPath,
		masterPath: masterPath,
		stdout:     stdout,
		stderr:     stderr,
		stdin:      stdin,
	}
	return a.dispatch(args)
}

func (a *app) dispatch(args []string) int {
	if len(args) == 0 {
		a.usage()
		return exitUsage
	}
	switch args[0] {
	case "token":
		return a.cmdToken(args[1:])
	case "gateway-key":
		return a.cmdGatewayKey(args[1:])
	case "provider-key":
		return a.cmdProviderKey(args[1:])
	case "grok":
		return a.cmdGrok(args[1:])
	case "audit":
		return a.cmdAudit(args[1:])
	case "db":
		return a.cmdDB(args[1:])
	case "help", "-h", "--help":
		a.usage()
		return exitOK
	default:
		fmt.Fprintf(a.stderr, "unknown command %q\n", args[0])
		a.usage()
		return exitUsage
	}
}

func (a *app) openStore() (*store.Store, error) {
	return store.Open(a.dbPath)
}

func (a *app) masterKey() ([]byte, error) {
	return secrets.LoadOrCreate(a.masterPath)
}

func (a *app) cmdToken(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: token init|rotate|verify")
		return exitUsage
	}
	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.stderr, "open db: %v\n", err)
		return exitError
	}
	defer st.Close()
	auth := adminauth.NewService(st.DB(), 24*time.Hour)
	switch args[0] {
	case "init":
		raw, err := auth.Init()
		if err != nil {
			if errors.Is(err, adminauth.ErrTokenAlreadySet) {
				fmt.Fprintln(a.stderr, "admin token already initialized")
			} else {
				fmt.Fprintf(a.stderr, "token init: %v\n", err)
			}
			return exitError
		}
		fmt.Fprintln(a.stdout, raw)
		return exitOK
	case "rotate":
		raw, err := auth.Rotate()
		if err != nil {
			fmt.Fprintf(a.stderr, "token rotate: %v\n", err)
			return exitError
		}
		fmt.Fprintln(a.stdout, raw)
		return exitOK
	case "verify":
		raw, err := a.readTokenForVerify(args[1:])
		if err != nil {
			fmt.Fprintf(a.stderr, "%v\n", err)
			return exitUsage
		}
		ok, err := auth.Verify(raw)
		if err != nil {
			fmt.Fprintf(a.stderr, "token verify: %v\n", err)
			return exitError
		}
		if ok {
			fmt.Fprintln(a.stdout, "valid")
		} else {
			fmt.Fprintln(a.stdout, "invalid")
		}
		return exitOK
	default:
		fmt.Fprintf(a.stderr, "unknown token subcommand %q\n", args[0])
		return exitUsage
	}
}

func (a *app) readTokenForVerify(flags []string) (string, error) {
	var fromFlag string
	for i := 0; i < len(flags); i++ {
		if flags[i] == "--token" && i+1 < len(flags) {
			fromFlag = flags[i+1]
			break
		}
	}
	if fromFlag != "" {
		return strings.TrimSpace(fromFlag), nil
	}
	line, err := readLine(a.stdin)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (a *app) cmdGatewayKey(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: gateway-key create|list|disable|enable|revoke|delete")
		return exitUsage
	}
	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.stderr, "open db: %v\n", err)
		return exitError
	}
	defer st.Close()
	gk := gatewaykeys.NewService(st.DB())
	switch args[0] {
	case "create":
		name, ok := flagValue(args[1:], "--name")
		if !ok || name == "" {
			fmt.Fprintln(a.stderr, "gateway-key create requires --name")
			return exitUsage
		}
		raw, _, err := gk.Create(name)
		if err != nil {
			fmt.Fprintf(a.stderr, "gateway-key create: %v\n", err)
			return exitError
		}
		fmt.Fprintln(a.stdout, raw)
		return exitOK
	case "list":
		keys, err := gk.List()
		if err != nil {
			fmt.Fprintf(a.stderr, "gateway-key list: %v\n", err)
			return exitError
		}
		w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tPREFIX\tFINGERPRINT\tENABLED\tCREATED_AT\tLAST_USED_AT")
		for _, k := range keys {
			last := ""
			if k.LastUsedAt != nil {
				last = *k.LastUsedAt
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%v\t%s\t%s\n",
				k.ID, k.Name, k.Prefix, k.Fingerprint, k.Enabled, k.CreatedAt, last)
		}
		_ = w.Flush()
		return exitOK
	case "disable", "enable", "revoke", "delete":
		id, ok := flagInt64(args[1:], "--id")
		if !ok {
			fmt.Fprintf(a.stderr, "gateway-key %s requires --id\n", args[0])
			return exitUsage
		}
		var err error
		switch args[0] {
		case "disable":
			err = gk.Disable(id)
		case "enable":
			err = gk.Enable(id)
		case "revoke":
			err = gk.Revoke(id)
		case "delete":
			err = gk.Delete(id)
		}
		if err != nil {
			fmt.Fprintf(a.stderr, "gateway-key %s: %v\n", args[0], err)
			return exitError
		}
		return exitOK
	default:
		fmt.Fprintf(a.stderr, "unknown gateway-key subcommand %q\n", args[0])
		return exitUsage
	}
}

func (a *app) cmdProviderKey(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: provider-key add|list|disable|enable|reset-cooldown|delete")
		return exitUsage
	}
	mk, err := a.masterKey()
	if err != nil {
		fmt.Fprintf(a.stderr, "master key: %v\n", err)
		return exitError
	}
	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.stderr, "open db: %v\n", err)
		return exitError
	}
	defer st.Close()
	repo := providers.NewKeyRepo(st.DB(), mk)
	switch args[0] {
	case "add":
		provider, okP := flagValue(args[1:], "--provider")
		name, okN := flagValue(args[1:], "--name")
		if !okP || !okN || provider == "" || name == "" {
			fmt.Fprintln(a.stderr, "provider-key add requires --provider and --name")
			return exitUsage
		}
		rawKey, err := readLine(a.stdin)
		if err != nil {
			fmt.Fprintf(a.stderr, "read provider key from stdin: %v\n", err)
			return exitError
		}
		rawKey = strings.TrimSpace(rawKey)
		if rawKey == "" {
			fmt.Fprintln(a.stderr, "empty provider key")
			return exitUsage
		}
		d, err := repo.Add(provider, name, rawKey)
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-key add: %v\n", err)
			return exitError
		}
		fmt.Fprintf(a.stdout, "id=%d provider=%s name=%s prefix=%s fingerprint=%s\n",
			d.ID, d.Provider, d.Name, d.KeyPrefix, d.Fingerprint)
		return exitOK
	case "list":
		all, err := repo.ListAll()
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-key list: %v\n", err)
			return exitError
		}
		w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tPROVIDER\tNAME\tPREFIX\tFINGERPRINT\tENABLED\tCOOLDOWN_UNTIL")
		for _, k := range all {
			cd := ""
			if k.CooldownUntil != nil {
				cd = *k.CooldownUntil
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%v\t%s\n",
				k.ID, k.Provider, k.Name, k.KeyPrefix, k.Fingerprint, k.Enabled, cd)
		}
		_ = w.Flush()
		return exitOK
	case "disable", "enable", "reset-cooldown", "delete":
		id, ok := flagInt64(args[1:], "--id")
		if !ok {
			fmt.Fprintf(a.stderr, "provider-key %s requires --id\n", args[0])
			return exitUsage
		}
		var err error
		switch args[0] {
		case "disable":
			err = repo.Disable(id)
		case "enable":
			err = repo.Enable(id)
		case "reset-cooldown":
			err = repo.ResetCooldown(id)
		case "delete":
			err = repo.Delete(id)
		}
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-key %s: %v\n", args[0], err)
			return exitError
		}
		return exitOK
	default:
		fmt.Fprintf(a.stderr, "unknown provider-key subcommand %q\n", args[0])
		return exitUsage
	}
}

func (a *app) cmdGrok(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: grok set-base-url|get-base-url")
		return exitUsage
	}
	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.stderr, "open db: %v\n", err)
		return exitError
	}
	defer st.Close()
	settings := providers.NewSettingsRepo(st.DB())
	switch args[0] {
	case "set-base-url":
		if len(args) < 2 {
			fmt.Fprintln(a.stderr, "grok set-base-url requires <url>")
			return exitUsage
		}
		url := strings.TrimSpace(args[1])
		if err := settings.SetBaseURL(providers.ProviderGrok, url); err != nil {
			fmt.Fprintf(a.stderr, "grok set-base-url: %v\n", err)
			return exitError
		}
		return exitOK
	case "get-base-url":
		url, err := settings.GetBaseURL(providers.ProviderGrok)
		if err != nil {
			fmt.Fprintf(a.stderr, "grok get-base-url: %v\n", err)
			return exitError
		}
		fmt.Fprintln(a.stdout, url)
		return exitOK
	default:
		fmt.Fprintf(a.stderr, "unknown grok subcommand %q\n", args[0])
		return exitUsage
	}
}

func (a *app) cmdAudit(args []string) int {
	if len(args) == 0 || args[0] != "tail" {
		fmt.Fprintln(a.stderr, "usage: audit tail [--limit N]")
		return exitUsage
	}
	limit := 50
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--limit" && i+1 < len(rest) {
			n, err := strconv.Atoi(rest[i+1])
			if err != nil || n < 1 {
				fmt.Fprintln(a.stderr, "invalid --limit")
				return exitUsage
			}
			limit = n
			break
		}
	}
	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.stderr, "open db: %v\n", err)
		return exitError
	}
	defer st.Close()
	repo := audit.NewAuditRepo(st.DB())
	events, err := repo.List(audit.ListFilter{})
	if err != nil {
		fmt.Fprintf(a.stderr, "audit tail: %v\n", err)
		return exitError
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tOCCURRED_AT\tACTOR\tACTION\tTARGET\tDETAIL")
	for _, e := range events {
		actor := e.ActorKind
		if e.ActorID != nil {
			actor += ":" + *e.ActorID
		}
		target := ""
		if e.TargetKind != nil {
			target = *e.TargetKind
			if e.TargetID != nil {
				target += "/" + *e.TargetID
			}
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
			e.ID, e.OccurredAt, actor, e.Action, target, e.DetailRedacted)
	}
	_ = w.Flush()
	return exitOK
}

func (a *app) cmdDB(args []string) int {
	if len(args) == 0 || args[0] != "migrate" {
		fmt.Fprintln(a.stderr, "usage: db migrate")
		return exitUsage
	}
	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.stderr, "db migrate: %v\n", err)
		return exitError
	}
	defer st.Close()
	fmt.Fprintln(a.stdout, "migrations applied")
	return exitOK
}

func (a *app) usage() {
	fmt.Fprintln(a.stderr, `guda-gateway-admin — local gateway control plane CLI

Global flags (before subcommand):
  --db PATH          SQLite database (default /var/lib/code-guda-gateway/gateway.db)
  --master-key PATH  Master key file (default /etc/code-guda-gateway/master.key)

Commands:
  token init|rotate|verify
  gateway-key create --name NAME | list | disable|enable|revoke|delete --id ID
  provider-key add --provider grok|tavily|firecrawl --name NAME (key on stdin)
  provider-key list | disable|enable|reset-cooldown|delete --id ID
  grok set-base-url URL | get-base-url
  audit tail [--limit N]
  db migrate`)
}

func flagValue(args []string, name string) (string, bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func flagInt64(args []string, name string) (int64, bool) {
	v, ok := flagValue(args, name)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func readLine(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return sc.Text(), nil
}