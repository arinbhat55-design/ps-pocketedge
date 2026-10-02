package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func route(base string, segments ...string) (string, error) {
	for _, s := range segments {
		if !validSegment(s) {
			return "", fmt.Errorf("invalid path ID or name %q", s)
		}
		base += "/" + url.PathEscape(s)
	}
	return base, nil
}

func readInput(cmd *cobra.Command, file string) ([]byte, error) {
	var r io.Reader = cmd.InOrStdin()
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxInput {
		return nil, fmt.Errorf("input exceeds 4 MiB")
	}
	return b, nil
}

func readObject(cmd *cobra.Command, file string) (map[string]any, error) {
	if file == "" {
		return nil, fmt.Errorf("--file is required (JSON request object; use - for stdin)")
	}
	b, err := readInput(cmd, file)
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if err = json.Unmarshal(b, &v); err != nil || v == nil {
		return nil, fmt.Errorf("input must be a JSON object")
	}
	return v, nil
}

func (o *options) api(cmd *cobra.Command, method, path string, body any) error {
	c, err := o.client(true)
	if err != nil {
		return err
	}
	var result any
	if err = c.call(cmd.Context(), method, path, body, &result); err != nil {
		return err
	}
	if m, ok := result.(map[string]any); ok {
		if success, exists := m["success"].(bool); exists && !success {
			return fmt.Errorf("operation failed: %v", m["error"])
		}
	}
	if result == nil {
		// Confirm empty (204) successes on stderr; stdout stays empty for scripts.
		fmt.Fprintln(cmd.ErrOrStderr(), "Done.")
		return nil
	}
	return printJSON(cmd.OutOrStdout(), result)
}

// text prints a non-JSON response body (logs, CSV) unchanged.
func (o *options) text(cmd *cobra.Command, method, path string, body any) error {
	c, err := o.client(true)
	if err != nil {
		return err
	}
	r, err := c.request(cmd.Context(), method, path, body)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	_, err = io.Copy(cmd.OutOrStdout(), r.Body)
	return err
}

// objectCommand exposes structured API options without losing advanced fields.
func objectCommand(o *options, use, short, method string, path func([]string) (string, error), count int) *cobra.Command {
	var file string
	c := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(count), RunE: func(cmd *cobra.Command, args []string) error {
		p, err := path(args)
		if err != nil {
			return err
		}
		body, err := readObject(cmd, file)
		if err != nil {
			return err
		}
		return o.api(cmd, method, p, body)
	}}
	c.Flags().StringVarP(&file, "file", "f", "", "JSON request file, or - for stdin")
	return c
}

func itemCommand(o *options, use, short, method, base string, suffix ...string) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		p, err := route(base, append(args, suffix...)...)
		if err != nil {
			return err
		}
		return o.api(cmd, method, p, nil)
	}}
}

func databasesCommand(o *options) *cobra.Command {
	group := &cobra.Command{Use: "databases", Short: "Manage database instances, credentials, and backup policies"}
	group.AddCommand(&cobra.Command{Use: "list", Short: "List database instances", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return o.list(cmd, "/api/databases", []string{"id", "name", "engine", "version", "serverId", "phase"})
	}}, &cobra.Command{Use: "engines", Short: "List available database engines and supported options", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return o.api(cmd, "GET", "/api/database-engines", nil)
	}}, itemCommand(o, "inspect <database-id>", "Inspect a database instance", "GET", "/api/databases"),
		itemCommand(o, "remove <database-id>", "Remove a database instance and its deployment", "DELETE", "/api/databases"),
		itemCommand(o, "credentials <database-id>", "List masked database credentials", "GET", "/api/databases", "credentials"),
		itemCommand(o, "logical-backup <database-id>", "Request an online logical PostgreSQL backup", "POST", "/api/databases", "logical-backups"))
	for _, operation := range []string{"create", "preview"} {
		var file, engine, server, name string
		c := &cobra.Command{Use: operation, Short: strings.Title(operation) + " a database instance", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			body := map[string]any{}
			var err error
			if file != "" {
				body, err = readObject(cmd, file)
				if err != nil {
					return err
				}
			}
			for flag, value := range map[string]string{"engine": engine, "server": server, "name": name} {
				if cmd.Flags().Changed(flag) {
					key := flag
					if key == "server" {
						key = "serverId"
					}
					body[key] = value
				}
			}
			for _, key := range []string{"engine", "serverId", "name"} {
				if v, ok := body[key].(string); !ok || strings.TrimSpace(v) == "" {
					return fmt.Errorf("%s is required (flag or --file)", key)
				}
			}
			path := "/api/databases"
			if operation == "preview" {
				path += "/preview"
			}
			return o.api(cmd, "POST", path, body)
		}}
		c.Flags().StringVarP(&file, "file", "f", "", "JSON database settings, or - for stdin")
		c.Flags().StringVar(&engine, "engine", "", "Engine ID from databases engines")
		c.Flags().StringVar(&server, "server", "", "Target server ID")
		c.Flags().StringVar(&name, "name", "", "Instance name")
		group.AddCommand(c)
	}
	for _, spec := range []struct{ use, short, method, suffix string }{
		{"configure <database-id>", "Change version, CPU, memory, or storage", "PATCH", "configuration"},
		{"backup-policy <database-id>", "Configure backup scheduling and retention", "PUT", "backup-policy"},
		{"temporary-credentials <database-id>", "Create a temporary database credential", "POST", "temporary-credentials"},
	} {
		group.AddCommand(objectCommand(o, spec.use, spec.short, spec.method, func(args []string) (string, error) { return route("/api/databases", args[0], spec.suffix) }, 1))
	}
	for _, op := range []string{"refresh", "migrate"} {
		var backup string
		c := &cobra.Command{Use: op + " <database-id> --backup <backup-id>", Short: "Restore a backup into a target database", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if backup == "" {
				return fmt.Errorf("--backup is required")
			}
			p, err := route("/api/databases", args[0], op+"-from-backup")
			if err != nil {
				return err
			}
			return o.api(cmd, "POST", p, map[string]string{"backupId": backup})
		}}
		c.Flags().StringVar(&backup, "backup", "", "Source backup ID (refresh: physical; migrate: logical)")
		group.AddCommand(c)
	}
	group.AddCommand(databaseBackupCommand(o), postgresCommand(o), secretsCommand(o))
	return group
}

func databaseBackupCommand(o *options) *cobra.Command {
	var consistent bool
	c := &cobra.Command{Use: "backup <database-id>", Short: "Request a physical database backup", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		p, err := route("/api/databases", args[0], "backups")
		if err != nil {
			return err
		}
		body := map[string]any{}
		if cmd.Flags().Changed("consistent") {
			body["consistent"] = consistent
		}
		return o.api(cmd, "POST", p, body)
	}}
	c.Flags().BoolVar(&consistent, "consistent", false, "Stop containers during backup; omitted uses the database policy")
	return c
}

func postgresCommand(o *options) *cobra.Command {
	g := &cobra.Command{Use: "postgres", Short: "Inspect and administer PostgreSQL"}
	for _, op := range []string{"overview", "metrics", "alerts", "sessions", "slow-queries", "sizes"} {
		g.AddCommand(itemCommand(o, op+" <database-id>", "Show PostgreSQL "+op, "GET", "/api/databases", "postgres", op))
	}
	g.AddCommand(itemCommand(o, "connection-test <database-id>", "Test the PostgreSQL connection", "POST", "/api/databases", "postgres", "connection-test"),
		itemCommand(o, "enable-insights <database-id>", "Enable PostgreSQL query insights", "POST", "/api/databases", "postgres", "query-insights", "enable"))
	for _, spec := range []struct{ op, method, suffix string }{
		{"create-database", "POST", "databases"}, {"create-user", "POST", "users"}, {"connection-limit", "PUT", "connection-limit"},
	} {
		g.AddCommand(objectCommand(o, spec.op+" <database-id>", "Submit PostgreSQL "+spec.op+" settings", spec.method, func(args []string) (string, error) { return route("/api/databases", args[0], "postgres", spec.suffix) }, 1))
	}
	var queryFile string
	query := &cobra.Command{Use: "query <database-id>", Short: "Run SQL and print the result as CSV", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		p, err := route("/api/databases", args[0], "postgres", "query")
		if err != nil {
			return err
		}
		body, err := readObject(cmd, queryFile)
		if err != nil {
			return err
		}
		return o.text(cmd, "POST", p, body)
	}}
	query.Flags().StringVarP(&queryFile, "file", "f", "", "JSON query request, or - for stdin")
	g.AddCommand(query, &cobra.Command{Use: "remove-database <instance-id> <database-name>", Short: "Drop a PostgreSQL logical database", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		p, err := route("/api/databases", args[0], "postgres", "databases", args[1])
		if err != nil {
			return err
		}
		return o.api(cmd, "DELETE", p, nil)
	}}, &cobra.Command{Use: "terminate-session <instance-id> <pid>", Short: "Terminate a PostgreSQL backend session", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		p, err := route("/api/databases", args[0], "postgres", "sessions", args[1], "terminate")
		if err != nil {
			return err
		}
		return o.api(cmd, "POST", p, nil)
	}}, objectCommand(o, "permissions <instance-id> <username>", "Set a PostgreSQL user's database permissions", "PUT", func(args []string) (string, error) {
		return route("/api/databases", args[0], "postgres", "users", args[1], "permissions")
	}, 2))
	return g
}

func secretsCommand(o *options) *cobra.Command {
	g := &cobra.Command{Use: "secrets", Short: "Reveal, rotate, or revoke database credentials"}
	for _, op := range []string{"reveal", "rotate", "revoke"} {
		g.AddCommand(itemCommand(o, op+" <secret-id>", "Explicitly "+op+" a credential", "POST", "/api/secrets", op))
	}
	g.AddCommand(itemCommand(o, "grants <secret-id>", "List credential access grants", "GET", "/api/secrets", "grants"),
		objectCommand(o, "grant <secret-id> <user-id>", "Grant access to a credential (JSON expiresAt is optional)", "PUT", func(args []string) (string, error) { return route("/api/secrets", args[0], "grants", args[1]) }, 2),
		&cobra.Command{Use: "ungrant <secret-id> <user-id>", Short: "Remove a credential access grant", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			p, err := route("/api/secrets", args[0], "grants", args[1])
			if err != nil {
				return err
			}
			return o.api(cmd, "DELETE", p, nil)
		}})
	return g
}

func backupsCommand(o *options) *cobra.Command {
	g := &cobra.Command{Use: "backups", Short: "Create, inspect, list, and restore deployment backups"}
	var deployment string
	list := &cobra.Command{Use: "list --deployment <id>", Short: "List backups for a deployment", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if deployment == "" {
			return fmt.Errorf("--deployment is required")
		}
		p, err := route("/api/deployments", deployment, "backups")
		if err != nil {
			return err
		}
		return o.list(cmd, p, []string{"id", "status", "format", "createdAt"})
	}}
	list.Flags().StringVar(&deployment, "deployment", "", "Deployment ID")
	var target string
	var consistent bool
	create := &cobra.Command{Use: "create --deployment <id>", Short: "Request a deployment backup", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if target == "" {
			return fmt.Errorf("--deployment is required")
		}
		p, err := route("/api/deployments", target, "backups")
		if err != nil {
			return err
		}
		body := map[string]any{}
		if cmd.Flags().Changed("consistent") {
			body["consistent"] = consistent
		}
		return o.api(cmd, "POST", p, body)
	}}
	create.Flags().StringVar(&target, "deployment", "", "Deployment ID")
	create.Flags().BoolVar(&consistent, "consistent", false, "Stop containers during backup; omitted preserves API policy")
	g.AddCommand(list, create, itemCommand(o, "inspect <backup-id>", "Inspect backup status", "GET", "/api/backups"), itemCommand(o, "restore <backup-id>", "Restore a physical backup into its original deployment", "POST", "/api/backups", "restore"))
	return g
}

func imagesCommand(o *options) *cobra.Command {
	g := &cobra.Command{Use: "images", Short: "List, pull, inspect, remove, prune, and scan images"}
	var filter string
	l := &cobra.Command{Use: "list", Short: "List fleet images (offline servers may be omitted)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return o.list(cmd, "/api/images?"+url.Values{"serverId": {filter}}.Encode(), []string{"id", "repoTags", "sizeBytes", "serverId"})
	}}
	l.Flags().StringVar(&filter, "server", "", "Filter by server ID")
	g.AddCommand(l)
	for _, op := range []string{"pull", "inspect", "remove", "prune"} {
		var server, registry string
		var force, all bool
		use := op + " <image> --server <id>"
		count := 1
		if op == "prune" {
			use = "prune --server <id>"
			count = 0
		}
		c := &cobra.Command{Use: use, Short: strings.Title(op) + " Docker images on a server", Args: cobra.ExactArgs(count), RunE: func(cmd *cobra.Command, args []string) error {
			if server == "" {
				return fmt.Errorf("--server is required")
			}
			p, err := route("/api/servers", server, "images")
			if err != nil {
				return err
			}
			switch op {
			case "pull":
				return o.api(cmd, "POST", p+"/pull", map[string]string{"imageRef": args[0], "registryId": registry})
			case "prune":
				return o.api(cmd, "POST", p+"/prune", map[string]bool{"all": all})
			case "inspect", "remove":
				p, err = route(p, args[0])
				if err != nil {
					return err
				}
				if op == "inspect" {
					return o.api(cmd, "GET", p+"/inspect", nil)
				}
				return o.api(cmd, "DELETE", p+"?"+url.Values{"force": {fmt.Sprint(force)}}.Encode(), nil)
			}
			return nil
		}}
		c.Flags().StringVar(&server, "server", "", "Target server ID")
		if op == "pull" {
			c.Flags().StringVar(&registry, "registry", "", "Saved registry ID for authentication")
		}
		if op == "remove" {
			c.Flags().BoolVar(&force, "force", false, "Force image removal")
		}
		if op == "prune" {
			c.Flags().BoolVar(&all, "all", false, "Remove all unused images instead of only dangling images")
		}
		g.AddCommand(c)
	}
	g.AddCommand(objectCommand(o, "scan", "Scan an image using the control-plane scanner", "POST", func(_ []string) (string, error) { return "/api/images/scan", nil }, 0))
	return g
}
