package main

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	case "provider-endpoint":
		return a.cmdProviderEndpoint(args[1:])
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

type tokenEnvOptions struct {
	savePath string
	envKey   string
	fromEnv  bool
}

func recordCLIAudit(db *sql.DB, action, targetKind, targetID, detail string) {
	_ = audit.NewAuditRepo(db).Record(audit.AuditEvent{
		ActorKind:  "cli",
		Action:     action,
		TargetKind: targetKind,
		TargetID:   targetID,
		Detail:     detail,
	})
}

func (a *app) cmdToken(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: token init|sync-env|rotate [--from-env] [--save-env PATH] [--env-key NAME] | token verify [--token TOKEN]")
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
		envOpts, err := parseTokenEnvOptions(args[1:])
		if err != nil {
			fmt.Fprintf(a.stderr, "%v\n", err)
			return exitUsage
		}
		var raw string
		if envOpts.fromEnv {
			raw = strings.TrimSpace(os.Getenv(envOpts.envKey))
			if raw == "" {
				fmt.Fprintf(a.stderr, "token init --from-env: %s is empty\n", envOpts.envKey)
				return exitError
			}
			if err := auth.InitFromRaw(raw); err != nil {
				if errors.Is(err, adminauth.ErrTokenAlreadySet) {
					fmt.Fprintln(a.stderr, "admin token already initialized")
				} else if errors.Is(err, adminauth.ErrInvalidToken) {
					fmt.Fprintf(a.stderr, "token init --from-env: invalid %s (need gat_… or 16–128 printable chars)\n", envOpts.envKey)
				} else {
					fmt.Fprintf(a.stderr, "token init: %v\n", err)
				}
				return exitError
			}
		} else {
			raw, err = auth.Init()
			if err != nil {
				if errors.Is(err, adminauth.ErrTokenAlreadySet) {
					fmt.Fprintln(a.stderr, "admin token already initialized")
				} else {
					fmt.Fprintf(a.stderr, "token init: %v\n", err)
				}
				return exitError
			}
		}
		recordCLIAudit(st.DB(), "admin_token.init", "admin_token", "", "result=ok")
		return a.writeAdminToken(raw, envOpts)
	case "sync-env":
		// Align DB hash to GUDA_ADMIN_TOKEN (Coolify magic password). Init or replace.
		envOpts, err := parseTokenEnvOptions(args[1:])
		if err != nil {
			fmt.Fprintf(a.stderr, "%v\n", err)
			return exitUsage
		}
		raw := strings.TrimSpace(os.Getenv(envOpts.envKey))
		if raw == "" {
			fmt.Fprintf(a.stderr, "token sync-env: %s is empty\n", envOpts.envKey)
			return exitError
		}
		if err := auth.SetFromRaw(raw); err != nil {
			if errors.Is(err, adminauth.ErrInvalidToken) {
				fmt.Fprintf(a.stderr, "token sync-env: invalid %s (need gat_… or 16–128 printable chars)\n", envOpts.envKey)
			} else {
				fmt.Fprintf(a.stderr, "token sync-env: %v\n", err)
			}
			return exitError
		}
		// Default save path for Coolify volume mirror when not specified.
		if envOpts.savePath == "" {
			envOpts.savePath = "/var/lib/code-guda-gateway/admin-credentials.env"
		}
		recordCLIAudit(st.DB(), "admin_token.sync_env", "admin_token", "", "result=ok")
		return a.writeAdminToken(raw, envOpts)
	case "rotate":
		envOpts, err := parseTokenEnvOptions(args[1:])
		if err != nil {
			fmt.Fprintf(a.stderr, "%v\n", err)
			return exitUsage
		}
		var raw string
		if envOpts.fromEnv {
			raw = strings.TrimSpace(os.Getenv(envOpts.envKey))
			if raw == "" {
				fmt.Fprintf(a.stderr, "token rotate --from-env: %s is empty\n", envOpts.envKey)
				return exitError
			}
			if err := auth.SetFromRaw(raw); err != nil {
				if errors.Is(err, adminauth.ErrInvalidToken) {
					fmt.Fprintf(a.stderr, "token rotate --from-env: invalid %s\n", envOpts.envKey)
				} else {
					fmt.Fprintf(a.stderr, "token rotate: %v\n", err)
				}
				return exitError
			}
		} else {
			raw, err = auth.Rotate()
			if err != nil {
				fmt.Fprintf(a.stderr, "token rotate: %v\n", err)
				return exitError
			}
		}
		recordCLIAudit(st.DB(), "admin_token.rotate", "admin_token", "", "result=ok")
		return a.writeAdminToken(raw, envOpts)
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

func parseTokenEnvOptions(flags []string) (tokenEnvOptions, error) {
	opts := tokenEnvOptions{envKey: "GUDA_ADMIN_TOKEN"}
	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case "--from-env":
			opts.fromEnv = true
		case "--save-env":
			if i+1 >= len(flags) || strings.TrimSpace(flags[i+1]) == "" {
				return opts, errors.New("token --save-env requires PATH")
			}
			opts.savePath = flags[i+1]
			i++
		case "--env-key":
			if i+1 >= len(flags) || strings.TrimSpace(flags[i+1]) == "" {
				return opts, errors.New("token --env-key requires NAME")
			}
			opts.envKey = strings.TrimSpace(flags[i+1])
			i++
		default:
			return opts, fmt.Errorf("unknown token flag %q", flags[i])
		}
	}
	return opts, nil
}

func (a *app) writeAdminToken(raw string, opts tokenEnvOptions) int {
	fmt.Fprintln(a.stdout, raw)
	if opts.savePath == "" {
		return exitOK
	}
	path, err := expandHomePath(opts.savePath)
	if err != nil {
		fmt.Fprintf(a.stderr, "save env: %v\n", err)
		return exitError
	}
	if err := writeEnvBinding(path, opts.envKey, raw); err != nil {
		fmt.Fprintf(a.stderr, "save env: %v\n", err)
		return exitError
	}
	fmt.Fprintf(a.stderr, "saved %s to %s\n", opts.envKey, path)
	return exitOK
}

func expandHomePath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func writeEnvBinding(path, key, value string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("env key is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create env directory: %w", err)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read env file: %w", err)
	}
	binding := key + "=" + value
	lines := strings.SplitAfter(string(existing), "\n")
	var b strings.Builder
	replaced := false
	for _, line := range lines {
		if line == "" {
			continue
		}
		trimmed := strings.TrimRight(line, "\r\n")
		lineEnding := strings.TrimPrefix(line, trimmed)
		if envLineMatchesKey(trimmed, key) {
			if lineEnding == "" {
				lineEnding = "\n"
			}
			b.WriteString(binding)
			b.WriteString(lineEnding)
			replaced = true
			continue
		}
		b.WriteString(line)
	}
	if !replaced {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(binding)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write env file: %w", err)
	}
	return os.Chmod(path, 0o600)
}

func envLineMatchesKey(line, key string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, key+"=") || strings.HasPrefix(trimmed, "export "+key+"=")
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
		raw, display, err := gk.Create(name)
		if err != nil {
			fmt.Fprintf(a.stderr, "gateway-key create: %v\n", err)
			return exitError
		}
		recordCLIAudit(st.DB(), "gateway_key.create", "gateway_key", strconv.FormatInt(display.ID, 10), "name="+name+";result=ok")
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
		recordCLIAudit(st.DB(), "gateway_key."+args[0], "gateway_key", strconv.FormatInt(id, 10), "result=ok")
		return exitOK
	default:
		fmt.Fprintf(a.stderr, "unknown gateway-key subcommand %q\n", args[0])
		return exitUsage
	}
}

func (a *app) cmdProviderKey(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: provider-key add|list|disable|enable|archive|restore|reset-cooldown|reset-selection|demote|delete")
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
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(a.stderr, "empty provider key")
				return exitUsage
			}
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
		recordCLIAudit(st.DB(), "provider_key.add", "provider_key", strconv.FormatInt(d.ID, 10), "provider="+provider+";name="+name+";result=ok")
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
	case "disable", "enable", "archive", "restore", "reset-cooldown", "reset-selection", "demote", "delete":
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
		case "archive":
			err = repo.Archive(id)
		case "restore":
			err = repo.RestoreArchived(id)
		case "reset-cooldown":
			err = repo.ResetCooldown(id)
		case "reset-selection":
			err = repo.ResetSelection(id)
		case "demote":
			err = repo.DemoteToEnd(id)
		case "delete":
			err = repo.Delete(id)
		}
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-key %s: %v\n", args[0], err)
			return exitError
		}
		action := strings.ReplaceAll(args[0], "-", "_")
		recordCLIAudit(st.DB(), "provider_key."+action, "provider_key", strconv.FormatInt(id, 10), "result=ok")
		return exitOK
	default:
		fmt.Fprintf(a.stderr, "unknown provider-key subcommand %q\n", args[0])
		return exitUsage
	}
}

func (a *app) cmdProviderEndpoint(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: provider-endpoint add|list|set-base-url|rotate-key|set-quota|rotate-quota-key|disable|enable|archive|restore|reset-cooldown|reset-selection|demote|delete")
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
		baseURL, okU := flagValue(args[1:], "--base-url")
		if !okP || !okN || !okU || provider == "" || name == "" || strings.TrimSpace(baseURL) == "" {
			fmt.Fprintln(a.stderr, "provider-endpoint add requires --provider, --name, and --base-url")
			return exitUsage
		}
		rawKey, err := readLine(a.stdin)
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(a.stderr, "empty provider key")
				return exitUsage
			}
			fmt.Fprintf(a.stderr, "read provider key from stdin: %v\n", err)
			return exitError
		}
		rawKey = strings.TrimSpace(rawKey)
		if rawKey == "" {
			fmt.Fprintln(a.stderr, "empty provider key")
			return exitUsage
		}

		quotaMode, hasMode := flagValue(args[1:], "--quota-mode")
		quotaFlow, hasFlow := flagValue(args[1:], "--quota-flow")
		quotaBaseURL, _ := flagValue(args[1:], "--quota-base-url")
		quotaKeyFile, hasQuotaKeyFile := flagValue(args[1:], "--quota-key-file")

		var d providers.DisplayProviderKey
		if hasMode || hasFlow || strings.TrimSpace(quotaBaseURL) != "" || hasQuotaKeyFile {
			mode := providers.QuotaMode(strings.TrimSpace(quotaMode))
			flow := providers.QuotaFlow(strings.TrimSpace(quotaFlow))
			if !hasMode || !hasFlow {
				defMode, defFlow, derr := providers.DefaultQuotaConfig(provider)
				if derr != nil {
					fmt.Fprintf(a.stderr, "provider-endpoint add: %v\n", derr)
					return exitError
				}
				if !hasMode {
					mode = defMode
				}
				if !hasFlow {
					flow = defFlow
				}
			}
			quota := providers.EndpointQuotaInput{
				Mode:    mode,
				Flow:    flow,
				BaseURL: strings.TrimSpace(quotaBaseURL),
			}
			if mode == providers.QuotaSeparateCredentials {
				if !hasQuotaKeyFile || strings.TrimSpace(quotaKeyFile) == "" {
					fmt.Fprintln(a.stderr, "provider-endpoint add with separate_credentials requires --quota-key-file PATH")
					return exitUsage
				}
				qKey, qerr := readSecretFile(quotaKeyFile)
				if qerr != nil {
					fmt.Fprintf(a.stderr, "provider-endpoint add: read --quota-key-file: %v\n", qerr)
					return exitError
				}
				quota.RawKey = qKey
			} else if hasQuotaKeyFile {
				fmt.Fprintln(a.stderr, "provider-endpoint add: --quota-key-file is only valid with --quota-mode separate_credentials")
				return exitUsage
			}
			d, err = repo.AddEndpointWithQuota(provider, name, baseURL, rawKey, quota)
		} else {
			d, err = repo.AddEndpoint(provider, name, baseURL, rawKey)
		}
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint add: %v\n", err)
			return exitError
		}
		fmt.Fprintf(a.stdout, "id=%d provider=%s name=%s base_url=%s prefix=%s fingerprint=%s quota_mode=%s quota_flow=%s",
			d.ID, d.Provider, d.Name, d.BaseURL, d.KeyPrefix, d.Fingerprint, d.QuotaMode, d.QuotaFlow)
		if d.QuotaBaseURL != nil {
			fmt.Fprintf(a.stdout, " quota_base_url=%s", *d.QuotaBaseURL)
		}
		fmt.Fprintf(a.stdout, " quota_key_configured=%v\n", d.QuotaKeyConfigured)
		detail := "provider=" + provider + ";name=" + name + ";base_url=" + d.BaseURL +
			";quota_mode=" + string(d.QuotaMode) + ";quota_flow=" + string(d.QuotaFlow) + ";result=ok"
		recordCLIAudit(st.DB(), "provider_endpoint.add", "provider_endpoint", strconv.FormatInt(d.ID, 10), detail)
		return exitOK
	case "list":
		all, err := repo.ListAll()
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint list: %v\n", err)
			return exitError
		}
		w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tPROVIDER\tNAME\tBASE_URL\tPREFIX\tFINGERPRINT\tENABLED\tCOOLDOWN_UNTIL\tQUOTA_MODE\tQUOTA_FLOW\tQUOTA_BASE_URL\tQUOTA_CONFIGURED\tQUOTA_PREFIX\tQUOTA_FINGERPRINT")
		for _, k := range all {
			cd := ""
			if k.CooldownUntil != nil {
				cd = *k.CooldownUntil
			}
			qBase := ""
			if k.QuotaBaseURL != nil {
				qBase = *k.QuotaBaseURL
			}
			qPrefix := ""
			if k.QuotaKeyPrefix != nil {
				qPrefix = *k.QuotaKeyPrefix
			}
			qFP := ""
			if k.QuotaKeyFingerprint != nil {
				qFP = *k.QuotaKeyFingerprint
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%v\t%s\t%s\t%s\t%s\t%v\t%s\t%s\n",
				k.ID, k.Provider, k.Name, k.BaseURL, k.KeyPrefix, k.Fingerprint, k.Enabled, cd,
				k.QuotaMode, k.QuotaFlow, qBase, k.QuotaKeyConfigured, qPrefix, qFP)
		}
		_ = w.Flush()
		return exitOK
	case "set-base-url":
		id, ok := flagInt64(args[1:], "--id")
		url, okU := flagValue(args[1:], "--url")
		if !ok || !okU || strings.TrimSpace(url) == "" {
			fmt.Fprintln(a.stderr, "provider-endpoint set-base-url requires --id and --url")
			return exitUsage
		}
		row, err := repo.Get(id)
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint set-base-url: %v\n", err)
			return exitError
		}
		if err := repo.UpdateBaseURL(id, url); err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint set-base-url: %v\n", err)
			return exitError
		}
		updated, err := repo.Get(id)
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint set-base-url: %v\n", err)
			return exitError
		}
		fmt.Fprintf(a.stdout, "id=%d base_url=%s\n", id, updated.BaseURL)
		recordCLIAudit(st.DB(), "provider_endpoint.update_base_url", "provider_endpoint", strconv.FormatInt(id, 10), "provider="+row.Provider+";name="+row.Name+";base_url="+updated.BaseURL+";result=ok")
		return exitOK
	case "rotate-key":
		id, ok := flagInt64(args[1:], "--id")
		if !ok {
			fmt.Fprintln(a.stderr, "provider-endpoint rotate-key requires --id")
			return exitUsage
		}
		rawKey, err := readLine(a.stdin)
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(a.stderr, "empty provider key")
				return exitUsage
			}
			fmt.Fprintf(a.stderr, "read provider key from stdin: %v\n", err)
			return exitError
		}
		rawKey = strings.TrimSpace(rawKey)
		if rawKey == "" {
			fmt.Fprintln(a.stderr, "empty provider key")
			return exitUsage
		}
		row, err := repo.Get(id)
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint rotate-key: %v\n", err)
			return exitError
		}
		if err := repo.RotateKey(id, rawKey); err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint rotate-key: %v\n", err)
			return exitError
		}
		updated, err := repo.Get(id)
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint rotate-key: %v\n", err)
			return exitError
		}
		fmt.Fprintf(a.stdout, "id=%d prefix=%s fingerprint=%s\n", updated.ID, updated.KeyPrefix, updated.Fingerprint)
		recordCLIAudit(st.DB(), "provider_endpoint.rotate_key", "provider_endpoint", strconv.FormatInt(id, 10), "provider="+row.Provider+";name="+row.Name+";fingerprint="+updated.Fingerprint+";result=ok")
		return exitOK
	case "set-quota":
		id, ok := flagInt64(args[1:], "--id")
		modeStr, okM := flagValue(args[1:], "--mode")
		flowStr, okF := flagValue(args[1:], "--flow")
		if !ok || !okM || !okF || strings.TrimSpace(modeStr) == "" || strings.TrimSpace(flowStr) == "" {
			fmt.Fprintln(a.stderr, "provider-endpoint set-quota requires --id, --mode, and --flow")
			return exitUsage
		}
		baseURL, _ := flagValue(args[1:], "--base-url")
		row, err := repo.Get(id)
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint set-quota: %v\n", err)
			return exitError
		}
		if err := repo.UpdateEndpointQuota(id, providers.EndpointQuotaInput{
			Mode:    providers.QuotaMode(strings.TrimSpace(modeStr)),
			Flow:    providers.QuotaFlow(strings.TrimSpace(flowStr)),
			BaseURL: strings.TrimSpace(baseURL),
		}); err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint set-quota: %v\n", err)
			return exitError
		}
		updated, err := repo.Get(id)
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint set-quota: %v\n", err)
			return exitError
		}
		fmt.Fprintf(a.stdout, "id=%d quota_mode=%s quota_flow=%s", updated.ID, updated.QuotaMode, updated.QuotaFlow)
		if updated.QuotaBaseURL != nil {
			fmt.Fprintf(a.stdout, " quota_base_url=%s", *updated.QuotaBaseURL)
		}
		fmt.Fprintf(a.stdout, " quota_key_configured=%v\n", updated.QuotaKeyConfigured)
		detail := "provider=" + row.Provider + ";name=" + row.Name +
			";quota_mode=" + string(updated.QuotaMode) + ";quota_flow=" + string(updated.QuotaFlow) + ";result=ok"
		if updated.QuotaBaseURL != nil {
			detail += ";quota_base_url=" + *updated.QuotaBaseURL
		}
		recordCLIAudit(st.DB(), "provider_endpoint.update_quota", "provider_endpoint", strconv.FormatInt(id, 10), detail)
		return exitOK
	case "rotate-quota-key":
		id, ok := flagInt64(args[1:], "--id")
		if !ok {
			fmt.Fprintln(a.stderr, "provider-endpoint rotate-quota-key requires --id")
			return exitUsage
		}
		rawKey, err := readLine(a.stdin)
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(a.stderr, "empty quota key")
				return exitUsage
			}
			fmt.Fprintf(a.stderr, "read quota key from stdin: %v\n", err)
			return exitError
		}
		rawKey = strings.TrimSpace(rawKey)
		if rawKey == "" {
			fmt.Fprintln(a.stderr, "empty quota key")
			return exitUsage
		}
		row, err := repo.Get(id)
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint rotate-quota-key: %v\n", err)
			return exitError
		}
		if err := repo.RotateEndpointQuotaKey(id, rawKey); err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint rotate-quota-key: %v\n", err)
			return exitError
		}
		updated, err := repo.Get(id)
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint rotate-quota-key: %v\n", err)
			return exitError
		}
		qPrefix := ""
		if updated.QuotaKeyPrefix != nil {
			qPrefix = *updated.QuotaKeyPrefix
		}
		qFP := ""
		if updated.QuotaKeyFingerprint != nil {
			qFP = *updated.QuotaKeyFingerprint
		}
		fmt.Fprintf(a.stdout, "id=%d quota_key_prefix=%s quota_key_fingerprint=%s quota_key_configured=%v\n",
			updated.ID, qPrefix, qFP, updated.QuotaKeyConfigured)
		recordCLIAudit(st.DB(), "provider_endpoint.rotate_quota_key", "provider_endpoint", strconv.FormatInt(id, 10),
			"provider="+row.Provider+";name="+row.Name+";quota_fingerprint="+qFP+";result=ok")
		return exitOK
	case "disable", "enable", "archive", "restore", "reset-cooldown", "reset-selection", "demote", "delete":
		id, ok := flagInt64(args[1:], "--id")
		if !ok {
			fmt.Fprintf(a.stderr, "provider-endpoint %s requires --id\n", args[0])
			return exitUsage
		}
		var err error
		switch args[0] {
		case "disable":
			err = repo.Disable(id)
		case "enable":
			err = repo.Enable(id)
		case "archive":
			err = repo.Archive(id)
		case "restore":
			err = repo.RestoreArchived(id)
		case "reset-cooldown":
			err = repo.ResetCooldown(id)
		case "reset-selection":
			err = repo.ResetSelection(id)
		case "demote":
			err = repo.DemoteToEnd(id)
		case "delete":
			err = repo.Delete(id)
		}
		if err != nil {
			fmt.Fprintf(a.stderr, "provider-endpoint %s: %v\n", args[0], err)
			return exitError
		}
		action := strings.ReplaceAll(args[0], "-", "_")
		recordCLIAudit(st.DB(), "provider_endpoint."+action, "provider_endpoint", strconv.FormatInt(id, 10), "result=ok")
		return exitOK
	default:
		fmt.Fprintf(a.stderr, "unknown provider-endpoint subcommand %q\n", args[0])
		return exitUsage
	}
}

func (a *app) cmdGrok(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: grok set-base-url|get-base-url|set-quota-mode|get-quota-mode|set-admin-base-url|get-admin-base-url|set-admin-key")
		fmt.Fprintln(a.stderr, "note: global Grok quota settings (set-quota-mode, set-admin-base-url, set-admin-key) are deprecated through v0.4.x; prefer provider-endpoint set-quota / rotate-quota-key")
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
		recordCLIAudit(st.DB(), "provider_setting.update", "provider_setting", providers.ProviderGrok, "provider=grok;result=ok")
		return exitOK
	case "get-base-url":
		url, err := settings.GetBaseURL(providers.ProviderGrok)
		if err != nil {
			fmt.Fprintf(a.stderr, "grok get-base-url: %v\n", err)
			return exitError
		}
		fmt.Fprintln(a.stdout, url)
		return exitOK
	case "set-quota-mode":
		if len(args) < 2 {
			fmt.Fprintln(a.stderr, "grok set-quota-mode requires <unsupported|grok2api_admin>")
			return exitUsage
		}
		mode := strings.TrimSpace(args[1])
		if err := settings.SetGrokQuotaMode(mode); err != nil {
			fmt.Fprintf(a.stderr, "grok set-quota-mode: %v\n", err)
			return exitError
		}
		recordCLIAudit(st.DB(), "setting.update", "setting", "grok_quota_mode", "value="+mode+";result=ok")
		return exitOK
	case "get-quota-mode":
		mode, err := settings.GetGrokQuotaMode()
		if err != nil {
			fmt.Fprintf(a.stderr, "grok get-quota-mode: %v\n", err)
			return exitError
		}
		fmt.Fprintln(a.stdout, mode)
		return exitOK
	case "set-admin-base-url":
		if len(args) < 2 {
			fmt.Fprintln(a.stderr, "grok set-admin-base-url requires <url>")
			return exitUsage
		}
		url := strings.TrimSpace(args[1])
		if err := settings.SetGrok2APIAdminBaseURL(url); err != nil {
			fmt.Fprintf(a.stderr, "grok set-admin-base-url: %v\n", err)
			return exitError
		}
		recordCLIAudit(st.DB(), "setting.update", "setting", "grok2api_admin_base_url", "value="+url+";result=ok")
		return exitOK
	case "get-admin-base-url":
		url, err := settings.GetGrok2APIAdminBaseURL()
		if err != nil {
			fmt.Fprintf(a.stderr, "grok get-admin-base-url: %v\n", err)
			return exitError
		}
		fmt.Fprintln(a.stdout, url)
		return exitOK
	case "set-admin-key":
		mk, err := a.masterKey()
		if err != nil {
			fmt.Fprintf(a.stderr, "master key: %v\n", err)
			return exitError
		}
		rawKey, err := readLine(a.stdin)
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(a.stderr, "empty admin key")
				return exitUsage
			}
			fmt.Fprintf(a.stderr, "read admin key from stdin: %v\n", err)
			return exitError
		}
		rawKey = strings.TrimSpace(rawKey)
		if err := settings.SetGrok2APIAdminKey(mk, rawKey); err != nil {
			fmt.Fprintf(a.stderr, "grok set-admin-key: %v\n", err)
			return exitError
		}
		recordCLIAudit(st.DB(), "setting.update", "setting", "grok2api_admin_key_encrypted", "result=ok")
		fmt.Fprintln(a.stdout, "grok2api admin key updated successfully")
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
	events, err := repo.ListRecent(limit)
	if err != nil {
		fmt.Fprintf(a.stderr, "audit tail: %v\n", err)
		return exitError
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
	recordCLIAudit(st.DB(), "db.migrate", "db", "", "result=ok")
	fmt.Fprintln(a.stdout, "migrations applied")
	return exitOK
}

func (a *app) usage() {
	fmt.Fprintln(a.stderr, `guda-gateway-admin — local gateway control plane CLI

Global flags (before subcommand):
  --db PATH          SQLite database (default /var/lib/code-guda-gateway/gateway.db)
  --master-key PATH  Master key file (default /etc/code-guda-gateway/master.key)

Commands:
  token init [--from-env] [--save-env PATH] [--env-key NAME]
  token sync-env [--save-env PATH] [--env-key NAME]   # set hash from GUDA_ADMIN_TOKEN (Coolify)
  token rotate [--from-env] [--save-env PATH] [--env-key NAME] | token verify [--token TOKEN]
  gateway-key create --name NAME | list | disable|enable|revoke|delete --id ID
  provider-key add --provider grok|tavily|firecrawl --name NAME (key on stdin only; never pass secrets as argv)
  provider-key list | disable|enable|archive|restore|reset-cooldown|reset-selection|demote|delete --id ID
  provider-endpoint add --provider grok|tavily|firecrawl --name NAME --base-url URL (key on stdin only)
    [--quota-mode disabled|endpoint_credentials|separate_credentials]
    [--quota-flow grok2api_admin|tavily_usage|firecrawl_credit_usage]
    [--quota-base-url URL] [--quota-key-file PATH]  (PATH required for separate_credentials; never pass raw keys as flags)
  provider-endpoint list | set-base-url --id ID --url URL | rotate-key --id ID (key on stdin)
  provider-endpoint set-quota --id ID --mode MODE --flow FLOW [--base-url URL]
  provider-endpoint rotate-quota-key --id ID (quota key on stdin)
  provider-endpoint disable|enable|archive|restore|reset-cooldown|reset-selection|demote|delete --id ID
  grok set-base-url URL | get-base-url | set-quota-mode MODE | get-quota-mode | set-admin-base-url URL | get-admin-base-url | set-admin-key
    (deprecated through v0.4.x: prefer provider-endpoint set-quota / rotate-quota-key for per-endpoint quota)
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

// readSecretFile loads a secret from PATH, trims surrounding whitespace, and
// rejects empty values. Never logs file contents.
func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "", errors.New("empty quota key")
	}
	return s, nil
}
