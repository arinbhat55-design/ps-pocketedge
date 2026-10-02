package cli

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func kubernetesCommand(o *options) *cobra.Command {
	g := &cobra.Command{Use: "kubernetes", Aliases: []string{"k8s"}, Short: "Manage clusters, workloads, pod logs, and Helm releases"}
	clusters := &cobra.Command{Use: "clusters", Short: "Register and manage Kubernetes clusters"}
	clusters.AddCommand(&cobra.Command{Use: "list", Short: "List registered clusters", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return o.list(cmd, "/api/kubernetes/clusters", []string{"id", "name", "apiServer", "createdAt"})
	}}, itemCommand(o, "remove <cluster-id>", "Unregister a cluster (does not delete the remote cluster)", "DELETE", "/api/kubernetes/clusters"),
		itemCommand(o, "delete-local <cluster-id>", "Delete a managed local kind cluster", "DELETE", "/api/kubernetes/local-clusters"))
	var name, kubeconfig string
	add := &cobra.Command{Use: "add --name <name> --kubeconfig <file>", Short: "Register a cluster using a kubeconfig", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if name == "" || kubeconfig == "" {
			return fmt.Errorf("--name and --kubeconfig are required")
		}
		b, err := readInput(cmd, kubeconfig)
		if err != nil {
			return err
		}
		return o.api(cmd, "POST", "/api/kubernetes/clusters", map[string]string{"name": name, "kubeconfig": string(b)})
	}}
	add.Flags().StringVar(&name, "name", "", "Cluster display name")
	add.Flags().StringVar(&kubeconfig, "kubeconfig", "", "Kubeconfig file, or - for stdin")
	var localName string
	local := &cobra.Command{Use: "create-local --name <name>", Short: "Create a kind cluster on the control-plane host", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if localName == "" {
			return fmt.Errorf("--name is required")
		}
		return o.api(cmd, "POST", "/api/kubernetes/local-clusters", map[string]string{"name": localName})
	}}
	local.Flags().StringVar(&localName, "name", "", "Cluster display name")
	clusters.AddCommand(add, local)
	var namespace string
	overview := &cobra.Command{Use: "overview <cluster-id>", Short: "Show nodes, pods, and workloads", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		p, err := route("/api/kubernetes/clusters", args[0], "overview")
		if err != nil {
			return err
		}
		ns := namespace
		if ns == "all" {
			ns = ""
		}
		return o.api(cmd, "GET", p+"?"+url.Values{"namespace": {ns}}.Encode(), nil)
	}}
	overview.Flags().StringVarP(&namespace, "namespace", "n", "default", "Namespace to inspect (all for every namespace)")
	g.AddCommand(clusters, overview, kubePodsCommand(o), kubeWorkloadsCommand(o), helmCommand(o))
	g.AddCommand(objectCommand(o, "ollama <cluster-id>", "Deploy Ollama using the API's model and resource options", "POST", func(args []string) (string, error) { return route("/api/kubernetes/clusters", args[0], "ai", "ollama") }, 1))
	return g
}

func kubePodsCommand(o *options) *cobra.Command {
	g := &cobra.Command{Use: "pods", Short: "Read Kubernetes pod logs"}
	var namespace, container string
	var lines int
	c := &cobra.Command{Use: "logs <pod> --cluster <cluster-id>", Short: "Read recent pod logs", Args: cobra.ExactArgs(1)}
	var cluster string
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if cluster == "" {
			return fmt.Errorf("--cluster is required")
		}
		if lines < 1 || lines > 1000 {
			return fmt.Errorf("--tail must be between 1 and 1000")
		}
		p, err := route("/api/kubernetes/clusters", cluster, "pods", namespace, args[0], "logs")
		if err != nil {
			return err
		}
		return o.text(cmd, "GET", p+"?"+url.Values{"container": {container}, "lines": {fmt.Sprint(lines)}}.Encode(), nil)
	}
	c.Flags().StringVar(&cluster, "cluster", "", "Cluster ID")
	c.Flags().StringVarP(&namespace, "namespace", "n", "default", "Pod namespace")
	c.Flags().StringVar(&container, "container", "", "Container name within the pod")
	c.Flags().IntVar(&lines, "tail", 200, "Recent log lines (1–1000)")
	g.AddCommand(c)
	return g
}

func kubeWorkloadsCommand(o *options) *cobra.Command {
	g := &cobra.Command{Use: "workloads", Short: "Create, update, remove, and roll back managed deployments"}
	for _, op := range []string{"create", "update", "remove", "revisions", "rollback"} {
		var cluster, namespace, file string
		var revision int64
		var dryRun bool
		count := 1
		use := op + " <name> --cluster <id>"
		if op == "create" {
			count = 0
			use = "create --cluster <id> --file <JSON>"
		}
		c := &cobra.Command{Use: use, Short: strings.Title(op) + " a managed workload", Args: cobra.ExactArgs(count), RunE: func(cmd *cobra.Command, args []string) error {
			if cluster == "" {
				return fmt.Errorf("--cluster is required")
			}
			p, err := route("/api/kubernetes/clusters", cluster, "workloads")
			if err != nil {
				return err
			}
			if op != "create" {
				p, err = route(p, namespace, args[0])
				if err != nil {
					return err
				}
			}
			if dryRun {
				p += "?dryRun=true"
			}
			switch op {
			case "create", "update":
				body, err := readObject(cmd, file)
				if err != nil {
					return err
				}
				method := "POST"
				if op == "update" {
					method = "PUT"
				}
				return o.api(cmd, method, p, body)
			case "remove":
				return o.api(cmd, "DELETE", p, nil)
			case "revisions":
				return o.api(cmd, "GET", p+"/revisions", nil)
			case "rollback":
				if revision < 1 {
					return fmt.Errorf("--revision must be positive")
				}
				return o.api(cmd, "POST", p+"/rollback", map[string]int64{"revision": revision})
			}
			return nil
		}}
		c.Flags().StringVar(&cluster, "cluster", "", "Cluster ID")
		if op != "create" {
			c.Flags().StringVarP(&namespace, "namespace", "n", "default", "Workload namespace")
		}
		if op == "create" || op == "update" {
			c.Flags().StringVarP(&file, "file", "f", "", "JSON workload settings (not a Kubernetes manifest), or - for stdin")
			c.Flags().BoolVar(&dryRun, "dry-run", false, "Validate through the Kubernetes dry-run API")
		}
		if op == "rollback" {
			c.Flags().Int64Var(&revision, "revision", 0, "Target workload revision")
		}
		g.AddCommand(c)
	}
	return g
}

func helmCommand(o *options) *cobra.Command {
	g := &cobra.Command{Use: "helm", Short: "List, install or upgrade, inspect history, and roll back Helm releases"}
	for _, op := range []string{"list", "install", "history", "rollback", "remove"} {
		var cluster, namespace, chart, values string
		var revision int
		count := 1
		use := op + " <release> --cluster <id>"
		if op == "list" {
			count = 0
			use = "list --cluster <id>"
		}
		c := &cobra.Command{Use: use, Short: strings.Title(op) + " Helm releases", Args: cobra.ExactArgs(count), RunE: func(cmd *cobra.Command, args []string) error {
			if cluster == "" {
				return fmt.Errorf("--cluster is required")
			}
			p, err := route("/api/kubernetes/clusters", cluster, "helm")
			if err != nil {
				return err
			}
			switch op {
			case "list":
				return o.api(cmd, "GET", p+"?"+url.Values{"namespace": {namespace}}.Encode(), nil)
			case "install":
				if chart == "" {
					return fmt.Errorf("--chart is required (.tgz file)")
				}
				b, err := readInput(cmd, chart)
				if err != nil {
					return err
				}
				v := map[string]any{}
				if values != "" {
					v, err = readObject(cmd, values)
					if err != nil {
						return err
					}
				}
				return o.api(cmd, "POST", p, map[string]any{"name": args[0], "namespace": namespace, "chartBase64": base64.StdEncoding.EncodeToString(b), "values": v})
			default:
				p, err = route(p, namespace, args[0])
				if err != nil {
					return err
				}
				if op == "history" {
					return o.api(cmd, "GET", p+"/history", nil)
				}
				if op == "rollback" {
					if revision < 1 {
						return fmt.Errorf("--revision must be positive")
					}
					return o.api(cmd, "POST", p+"/rollback", map[string]int{"revision": revision})
				}
				return o.api(cmd, "DELETE", p, nil)
			}
		}}
		c.Flags().StringVar(&cluster, "cluster", "", "Cluster ID")
		c.Flags().StringVarP(&namespace, "namespace", "n", "default", "Release namespace")
		if op == "install" {
			c.Flags().StringVar(&chart, "chart", "", "Packaged Helm chart (.tgz; up to 4 MiB)")
			c.Flags().StringVar(&values, "values", "", "JSON values file")
		}
		if op == "rollback" {
			c.Flags().IntVar(&revision, "revision", 0, "Target release revision")
		}
		g.AddCommand(c)
	}
	return g
}
