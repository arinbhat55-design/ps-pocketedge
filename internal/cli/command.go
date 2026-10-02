package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/version"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const maxInput = 4 << 20

// NewCommand returns a fresh command tree; callers own its input and output.
func NewCommand(in io.Reader, out, stderr io.Writer) *cobra.Command {
	o := &options{}
	root := &cobra.Command{Use: "pse", Short: "Manage PS-pocketEdge from your terminal", Version: version.String(), SilenceUsage: true, SilenceErrors: true}
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&o.url, "url", os.Getenv("PSE_URL"), "Control-plane URL (saved login or http://127.0.0.1:8080)")
	root.PersistentFlags().StringVar(&o.configPath, "config", os.Getenv("PSE_CONFIG"), "CLI configuration file")
	root.PersistentFlags().StringVar(&o.caFile, "ca-file", "", "PEM CA certificate for a private HTTPS control plane")
	root.PersistentFlags().BoolVar(&o.json, "json", false, "Print API results as JSON")
	root.PersistentFlags().DurationVar(&o.timeout, "timeout", 10*time.Minute, "HTTP request timeout (allows image pulls and cluster creation)")
	root.AddCommand(loginCommand(o), &cobra.Command{Use: "logout", Short: "Remove the saved login token", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := o.load()
		if err != nil {
			return err
		}
		c.Token = ""
		if err = o.save(c); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Saved login removed.")
		return nil
	}})
	servers := &cobra.Command{Use: "servers", Short: "List managed servers"}
	servers.AddCommand(&cobra.Command{Use: "list", Short: "List servers", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return o.list(cmd, "/api/servers", []string{"id", "name", "hostname", "status", "os", "arch"})
	}})
	root.AddCommand(servers, containersCommand(o), deployCommand(o))
	deployments := &cobra.Command{Use: "deployments", Short: "View deployment status"}
	deployments.AddCommand(&cobra.Command{Use: "list", Short: "List deployments", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return o.list(cmd, "/api/deployments", []string{"id", "sourceName", "serverId", "phase", "healthStatus"})
	}})
	root.AddCommand(deployments)
	root.AddCommand(databasesCommand(o), kubernetesCommand(o), backupsCommand(o), imagesCommand(o))
	return root
}

func loginCommand(o *options) *cobra.Command {
	var email string
	var stdin bool
	cmd := &cobra.Command{Use: "login --email <email>", Short: "Log in and save a session; prompts for a hidden password", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(email) == "" {
			return fmt.Errorf("--email is required")
		}
		c, err := o.client(false)
		if err != nil {
			return err
		}
		var password []byte
		if stdin {
			password, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), 65537))
			if len(password) > 65536 {
				return fmt.Errorf("password input is too large")
			}
			password = []byte(strings.TrimRight(string(password), "\r\n"))
		} else {
			f, ok := cmd.InOrStdin().(*os.File)
			if !ok || !term.IsTerminal(int(f.Fd())) {
				return fmt.Errorf("use --password-stdin for non-interactive login")
			}
			fmt.Fprint(cmd.ErrOrStderr(), "Password: ")
			password, err = term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(cmd.ErrOrStderr())
		}
		if err != nil {
			return err
		}
		if len(password) == 0 {
			return fmt.Errorf("password cannot be empty")
		}
		var result struct {
			Token string `json:"token"`
		}
		// Never send a previous session token when authenticating.
		c.Token = ""
		if err = c.call(cmd.Context(), "POST", "/api/auth/login", map[string]string{"email": email, "password": string(password)}, &result); err != nil {
			return err
		}
		if result.Token == "" {
			return fmt.Errorf("login returned an empty token")
		}
		c.Token = result.Token
		if c.CAFile != "" {
			c.CAFile, err = filepath.Abs(c.CAFile)
			if err != nil {
				return err
			}
		}
		if err = o.save(c.config); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s\n", c.URL)
		return nil
	}}
	cmd.Flags().StringVar(&email, "email", "", "Account email")
	cmd.Flags().BoolVar(&stdin, "password-stdin", false, "Read password from standard input")
	return cmd
}

func (o *options) list(cmd *cobra.Command, path string, columns []string) error {
	c, err := o.client(true)
	if err != nil {
		return err
	}
	var rows []map[string]any
	if err = c.call(cmd.Context(), "GET", path, nil, &rows); err != nil {
		return err
	}
	if o.json {
		if rows == nil {
			rows = []map[string]any{}
		}
		return printJSON(cmd.OutOrStdout(), rows)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, strings.ToUpper(strings.Join(columns, "\t")))
	for _, row := range rows {
		cells := make([]string, len(columns))
		for i, column := range columns {
			if row[column] != nil {
				cells[i] = safeCell(cellText(row[column]))
			}
		}
		fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	return w.Flush()
}

// cellText prints JSON numbers without exponents (sizes decode as float64)
// and lists as comma-separated values.
func cellText(v any) string {
	switch v := v.(type) {
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = cellText(item)
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprint(v)
}

func safeCell(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
}

func printJSON(out io.Writer, v any) error {
	e := json.NewEncoder(out)
	e.SetIndent("", "  ")
	return e.Encode(v)
}

func containersCommand(o *options) *cobra.Command {
	group := &cobra.Command{Use: "containers", Short: "List, inspect, control, and read logs from containers"}
	var filter string
	list := &cobra.Command{Use: "list", Short: "List containers across the fleet", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return o.list(cmd, "/api/containers?"+url.Values{"serverId": {filter}}.Encode(), []string{"containerId", "name", "image", "state", "serverId"})
	}}
	list.Flags().StringVar(&filter, "server", "", "Filter by server ID")
	group.AddCommand(list)
	for _, operation := range []string{"start", "stop", "restart", "inspect", "logs"} {
		var server string
		var tail int
		var follow bool
		cmd := &cobra.Command{Use: operation + " <container-id>", Short: strings.ToUpper(operation[:1]) + operation[1:] + " a container", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if operation == "logs" && (tail < 0 || int64(tail) > 2147483647) {
				return fmt.Errorf("--tail must be between 0 and 2147483647")
			}
			c, err := o.client(true)
			if err != nil {
				return err
			}
			if server == "" {
				server, err = resolveServer(cmd, c, args[0])
				if err != nil {
					return err
				}
			}
			if !validSegment(server) || !validSegment(args[0]) {
				return fmt.Errorf("invalid server or container ID")
			}
			path := "/api/servers/" + url.PathEscape(server) + "/containers/" + url.PathEscape(args[0])
			if operation == "logs" {
				if follow {
					return followLogs(cmd, c, path, tail, o.json)
				}
				resp, err := c.request(cmd.Context(), "GET", path+"/logs/download?"+url.Values{"tail": {fmt.Sprint(tail)}}.Encode(), nil)
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				_, err = io.Copy(cmd.OutOrStdout(), resp.Body)
				return err
			}
			var result map[string]any
			var body any
			if operation == "inspect" {
				path += "/inspect"
			} else {
				path += "/action"
				body = map[string]string{"action": operation}
			}
			if err = c.call(cmd.Context(), "POST", path, body, &result); err != nil {
				return err
			}
			if success, ok := result["success"].(bool); ok && !success {
				return fmt.Errorf("container %s failed: %v", operation, result["error"])
			}
			return printJSON(cmd.OutOrStdout(), result)
		}}
		cmd.Flags().StringVar(&server, "server", "", "Server ID (inferred from inventory when omitted)")
		if operation == "logs" {
			cmd.Flags().IntVar(&tail, "tail", 200, "Number of recent log lines; 0 requests all available lines")
			cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow live logs until interrupted")
		}
		group.AddCommand(cmd)
	}
	group.AddCommand(execCommand(o))
	return group
}

func validSegment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\?#\r\n")
}

func resolveServer(cmd *cobra.Command, c *client, id string) (string, error) {
	var rows []struct {
		ContainerID string `json:"containerId"`
		ServerID    string `json:"serverId"`
	}
	if err := c.call(cmd.Context(), "GET", "/api/containers", nil, &rows); err != nil {
		return "", err
	}
	var found string
	for _, row := range rows {
		if row.ContainerID == id {
			if found != "" {
				return "", fmt.Errorf("container exists on multiple servers; specify --server")
			}
			found = row.ServerID
		}
	}
	if found == "" {
		return "", fmt.Errorf("container not found in inventory; use its full ID or specify --server")
	}
	return found, nil
}

func deployCommand(o *options) *cobra.Command {
	var server, name, composeID string
	cmd := &cobra.Command{Use: "deploy [compose-file] --server <server-id>", Short: "Submit a Compose deployment through the existing approval workflow", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if server == "" {
			return fmt.Errorf("--server is required")
		}
		if (len(args) == 1) == (composeID != "") {
			return fmt.Errorf("provide either a Compose file or --compose-id")
		}
		c, err := o.client(true)
		if err != nil {
			return err
		}
		if len(args) == 1 {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			b, err := io.ReadAll(io.LimitReader(f, maxInput+1))
			f.Close()
			if err != nil {
				return err
			}
			if len(b) > maxInput {
				return fmt.Errorf("Compose file exceeds 4 MiB")
			}
			if name == "" {
				name = filepath.Base(args[0])
			}
			var created struct {
				ID string `json:"id"`
			}
			if err = c.call(cmd.Context(), "POST", "/api/compose-files", map[string]string{"name": name, "content": string(b)}, &created); err != nil {
				return err
			}
			composeID = created.ID
			if composeID == "" {
				return fmt.Errorf("Compose upload returned an empty ID")
			}
		}
		var result any
		if err = c.call(cmd.Context(), "POST", "/api/deployments", map[string]any{"serverId": server, "composeFileId": composeID}, &result); err != nil {
			return fmt.Errorf("deployment submission failed (saved Compose ID %s can be reused with --compose-id): %w", composeID, err)
		}
		return printJSON(cmd.OutOrStdout(), result)
	}}
	cmd.Flags().StringVar(&server, "server", "", "Target server ID")
	cmd.Flags().StringVar(&name, "name", "", "Name for the uploaded Compose file (defaults to filename)")
	cmd.Flags().StringVar(&composeID, "compose-id", "", "Deploy an existing saved Compose file instead of uploading")
	return cmd
}
