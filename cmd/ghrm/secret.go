package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/secrets"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

const secretUsage = `Usage: ghrm secret <set|delete|list> [--config path] [name]

  set <name>      Seal the value read from standard input (one line) under name
  delete <name>   Remove a secret
  list            Print the stored names (never the values)

Names in use: proxmox/token-secret, github/<credential name>.
`

// secretCmd manages the secrets sealed in the database (spec §10.4).
func secretCmd(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "set" && args[0] != "delete" && args[0] != "list") {
		fmt.Fprint(stderr, secretUsage)
		return 2
	}
	verb := args[0]
	fs := flag.NewFlagSet("secret "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "/etc/ghrm/ghrm.yaml", "configuration file")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	name := fs.Arg(0)
	if verb != "list" && name == "" {
		fmt.Fprint(stderr, secretUsage)
		return 2
	}
	cfg, err := config.Read(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, "ghrm.db"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer db.Close()
	vault, err := secrets.OpenVault(ctx, db, cfg.SecretKeyFile)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch verb {
	case "set":
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintln(stderr, err)
			return 1
		}
		value := strings.TrimRight(line, "\r\n")
		if value == "" {
			fmt.Fprintln(stderr, "secret: the value on standard input is empty")
			return 1
		}
		err = vault.Set(ctx, name, value)
	case "delete":
		err = vault.Delete(ctx, name)
	case "list":
		var names []string
		names, err = vault.Names(ctx)
		for _, n := range names {
			fmt.Fprintln(stdout, n)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// resolveFromVault fills missing secrets from the vault in the configuration's data_dir.
func resolveFromVault(ctx context.Context, cfg *config.Config) error {
	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, "ghrm.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	vault, err := secrets.OpenVault(ctx, db, cfg.SecretKeyFile)
	if err != nil {
		return err
	}
	return cfg.ResolveVaultSecrets(ctx, vault.Get)
}
