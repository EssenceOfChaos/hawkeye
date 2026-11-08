// Command oauthclient registers and manages the OAuth clients that may connect to HawkEye's MCP server (internal/oauth).
//
//	oauthclient create -name Claude -redirect https://claude.ai/api/mcp/auth_callback [-redirect …] [-public]
//	oauthclient list
//	oauthclient disable <client-id>
//
// create prints the client ID and, unless -public, its secret. The secret is shown once and stored only as a hash: give both to the client's
// operator (for Claude, enter them in Anthropic's directory portal). disable stops the client and revokes every connection it holds. It reads
// DATABASE_URL, like cmd/migrate.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"hawkeye/internal/database"
	"hawkeye/internal/email"
	"hawkeye/internal/oauth"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "oauthclient:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: oauthclient create|list|disable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Connect(ctx, url, 2, 10*time.Second)
	if err != nil {
		return err
	}
	defer pool.Close()
	svc := oauth.NewService(pool, nil, nil, &email.Memory{}, slog.New(slog.NewTextHandler(io.Discard, nil)), os.Getenv("APP_BASE_URL"))

	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("create", flag.ContinueOnError)
		name := fs.String("name", "", "the name people see when they connect it, e.g. Claude")
		public := fs.Bool("public", false, "no client secret (PKCE only)")
		var redirects multi
		fs.Var(&redirects, "redirect", "an allowed redirect URI (repeat for several)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(*name) == "" || len(redirects) == 0 {
			return fmt.Errorf("create needs -name and at least one -redirect")
		}
		c, secret, err := svc.CreateClient(ctx, strings.TrimSpace(*name), redirects, *public)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "client_id:     %s\n", c.ID)
		if secret != "" {
			fmt.Fprintf(out, "client_secret: %s\n(shown once: store it in the client's settings now)\n", secret)
		}
		return nil
	case "list":
		list, err := svc.ListClients(ctx)
		if err != nil {
			return err
		}
		for _, c := range list {
			kind := "confidential"
			if !c.Confidential {
				kind = "public"
			}
			fmt.Fprintf(out, "%s  %s  (%s)  %s\n", c.ID, c.Name, kind, strings.Join(c.RedirectURIs, " "))
		}
		return nil
	case "disable":
		if len(args) != 2 {
			return fmt.Errorf("usage: oauthclient disable <client-id>")
		}
		if err := svc.DisableClient(ctx, args[1]); err != nil {
			return err
		}
		fmt.Fprintln(out, "disabled; its connections were revoked")
		return nil
	}
	return fmt.Errorf("unknown command %q (want create, list or disable)", args[0])
}
