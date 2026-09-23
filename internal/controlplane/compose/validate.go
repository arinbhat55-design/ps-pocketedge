package compose

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// deploySupportedServiceKeys are the service fields the agent's deploy
// engine actually applies (see agent/docker.createContainer and friends).
// Anything else is valid Compose but silently has no effect when deployed,
// so Validate warns about it.
var deploySupportedServiceKeys = map[string]bool{
	"image":       true,
	"command":     true,
	"ports":       true,
	"environment": true,
	"volumes":     true,
	"restart":     true,
	"deploy":      true,
	"healthcheck": true,
	"labels":      true,
	"depends_on":  true,
	"networks":    true,
	"scale":       true,
}

// deploySupportedTopLevelKeys likewise for top-level keys.
var deploySupportedTopLevelKeys = map[string]bool{
	"version":  true,
	"name":     true,
	"services": true,
	"volumes":  true,
	"networks": true,
}

// validatedService is what Validate needs from one service mapping.
type validatedService struct {
	name      string
	node      *yaml.Node
	dependsOn []string
	networks  []string
	volumes   []string
	ports     []publishedPort
	replicas  int
}

type publishedPort struct {
	host     string
	protocol string
}

// Validate checks a structurally valid Compose document for problems that
// would make it fail, or behave unexpectedly, when deployed:
//
// Errors (the file can't be deployed as written):
//   - depends_on naming a service that isn't defined, the service itself,
//     or forming a dependency cycle.
//
// Warnings (conflicting or unsupported settings):
//   - two services publishing the same host port/protocol;
//   - a service with more than one replica that publishes a fixed host port;
//   - bind mounts (only named volumes are supported), named volumes or
//     networks referenced but not declared at the top level;
//   - a service with no image (build isn't supported);
//   - keys the deploy engine ignores (container_name, network_mode,
//     secrets, ...).
func Validate(doc *yaml.Node) (errs []string, warnings []string) {
	if doc == nil || doc.Kind != yaml.MappingNode {
		return nil, nil
	}
	var servicesNode *yaml.Node
	declaredVolumes := map[string]bool{}
	declaredNetworks := map[string]bool{"default": true}
	var ignoredTop []string
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key, value := doc.Content[i].Value, doc.Content[i+1]
		if !deploySupportedTopLevelKeys[key] && !strings.HasPrefix(key, "x-") {
			ignoredTop = append(ignoredTop, key)
		}
		switch key {
		case "services":
			servicesNode = value
		case "volumes":
			for _, k := range mappingKeys(value) {
				declaredVolumes[k] = true
			}
		case "networks":
			for _, k := range mappingKeys(value) {
				declaredNetworks[k] = true
			}
		}
	}
	if len(ignoredTop) > 0 {
		sort.Strings(ignoredTop)
		warnings = append(warnings, fmt.Sprintf("top-level %s not supported and will be ignored at deploy time", quoteList(ignoredTop)))
	}
	if servicesNode == nil || servicesNode.Kind != yaml.MappingNode {
		return nil, warnings
	}

	services := map[string]*validatedService{}
	var order []string
	for i := 0; i+1 < len(servicesNode.Content); i += 2 {
		name := servicesNode.Content[i].Value
		node := servicesNode.Content[i+1]
		if node.Kind != yaml.MappingNode {
			continue
		}
		svc := &validatedService{name: name, node: node, replicas: 1}
		var ignored []string
		hasImage, hasBuild := false, false
		for j := 0; j+1 < len(node.Content); j += 2 {
			key, value := node.Content[j].Value, node.Content[j+1]
			if !deploySupportedServiceKeys[key] && !strings.HasPrefix(key, "x-") {
				ignored = append(ignored, key)
			}
			switch key {
			case "image":
				hasImage = strings.TrimSpace(value.Value) != ""
			case "build":
				hasBuild = true
			case "depends_on":
				svc.dependsOn = sequenceOrMappingKeys(value)
			case "networks":
				svc.networks = sequenceOrMappingKeys(value)
			case "volumes":
				svc.volumes = volumeSources(value)
			case "ports":
				svc.ports = publishedPorts(value)
			case "scale":
				if n, err := strconv.Atoi(value.Value); err == nil {
					svc.replicas = n
				}
			case "deploy":
				if r := mappingValue(value, "replicas"); r != nil {
					if n, err := strconv.Atoi(r.Value); err == nil {
						svc.replicas = n
					}
				}
			}
		}
		if !hasImage {
			if hasBuild {
				warnings = append(warnings, fmt.Sprintf("service %q uses build, which isn't supported — it needs a prebuilt image", name))
			} else {
				warnings = append(warnings, fmt.Sprintf("service %q has no image", name))
			}
		}
		if len(ignored) > 0 {
			sort.Strings(ignored)
			warnings = append(warnings, fmt.Sprintf("service %q: %s not supported and will be ignored at deploy time", name, quoteList(ignored)))
		}
		services[name] = svc
		order = append(order, name)
	}

	// Service dependencies.
	for _, name := range order {
		for _, dep := range services[name].dependsOn {
			switch {
			case dep == name:
				errs = append(errs, fmt.Sprintf("service %q depends on itself", name))
			case services[dep] == nil:
				errs = append(errs, fmt.Sprintf("service %q depends on undefined service %q", name, dep))
			}
		}
	}
	if cycle := findDependencyCycle(order, services); cycle != "" {
		errs = append(errs, "dependency cycle: "+cycle)
	}

	// Conflicting / unsupported settings.
	portOwners := map[string][]string{}
	for _, name := range order {
		svc := services[name]
		for _, p := range svc.ports {
			key := p.host + "/" + p.protocol
			portOwners[key] = append(portOwners[key], name)
			if svc.replicas > 1 && !strings.Contains(p.host, "-") {
				warnings = append(warnings, fmt.Sprintf("service %q runs %d replicas but publishes fixed host port %s — only one replica can bind it", name, svc.replicas, p.host))
			}
		}
		for _, v := range svc.volumes {
			if isBindSource(v) {
				warnings = append(warnings, fmt.Sprintf("service %q mounts host path %q — bind mounts aren't supported, use a named volume", name, v))
			} else if v != "" && !declaredVolumes[v] {
				warnings = append(warnings, fmt.Sprintf("service %q uses volume %q, which isn't declared under top-level volumes", name, v))
			}
		}
		for _, n := range svc.networks {
			if !declaredNetworks[n] {
				warnings = append(warnings, fmt.Sprintf("service %q uses network %q, which isn't declared under top-level networks", name, n))
			}
		}
	}
	keys := make([]string, 0, len(portOwners))
	for k := range portOwners {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if owners := portOwners[k]; len(owners) > 1 {
			warnings = append(warnings, fmt.Sprintf("host port %s is published by more than one service (%s)", k, strings.Join(owners, ", ")))
		}
	}
	return errs, warnings
}

// findDependencyCycle returns "a -> b -> a" for the first cycle found, or
// "" if the dependency graph is acyclic. Dependencies on undefined
// services are skipped (reported separately).
func findDependencyCycle(order []string, services map[string]*validatedService) string {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var cycle string
	var visit func(name string, path []string) bool
	visit = func(name string, path []string) bool {
		switch state[name] {
		case done:
			return false
		case visiting:
			start := 0
			for i, p := range path {
				if p == name {
					start = i
					break
				}
			}
			cycle = strings.Join(append(append([]string{}, path[start:]...), name), " -> ")
			return true
		}
		state[name] = visiting
		for _, dep := range services[name].dependsOn {
			if services[dep] == nil || dep == name {
				continue
			}
			if visit(dep, append(path, name)) {
				return true
			}
		}
		state[name] = done
		return false
	}
	for _, name := range order {
		if visit(name, nil) {
			return cycle
		}
	}
	return ""
}

func mappingKeys(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	keys := make([]string, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		keys = append(keys, node.Content[i].Value)
	}
	return keys
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// sequenceOrMappingKeys reads depends_on/networks, which Compose allows as
// either a list of names or a mapping keyed by name.
func sequenceOrMappingKeys(node *yaml.Node) []string {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.SequenceNode:
		out := make([]string, 0, len(node.Content))
		for _, n := range node.Content {
			if n.Kind == yaml.ScalarNode {
				out = append(out, n.Value)
			}
		}
		return out
	case yaml.MappingNode:
		return mappingKeys(node)
	}
	return nil
}

// volumeSources returns the source of each volume entry (short "src:dst"
// or long {type, source, target} form); anonymous volumes have no source.
func volumeSources(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	var out []string
	for _, n := range node.Content {
		switch n.Kind {
		case yaml.ScalarNode:
			parts := strings.SplitN(n.Value, ":", 2)
			if len(parts) == 2 {
				out = append(out, parts[0])
			}
		case yaml.MappingNode:
			typ := ""
			if t := mappingValue(n, "type"); t != nil {
				typ = t.Value
			}
			if src := mappingValue(n, "source"); src != nil {
				if typ == "bind" && !isBindSource(src.Value) {
					out = append(out, "./"+src.Value)
				} else {
					out = append(out, src.Value)
				}
			}
		}
	}
	return out
}

func isBindSource(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") || strings.Contains(s, "$")
}

// publishedPorts returns every port entry that publishes to a host port.
func publishedPorts(node *yaml.Node) []publishedPort {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	var out []publishedPort
	for _, n := range node.Content {
		switch n.Kind {
		case yaml.ScalarNode:
			spec, protocol := n.Value, "tcp"
			if i := strings.LastIndex(spec, "/"); i >= 0 {
				spec, protocol = spec[:i], spec[i+1:]
			}
			parts := strings.Split(spec, ":")
			if len(parts) < 2 {
				continue
			}
			host := parts[len(parts)-2]
			if host != "" {
				out = append(out, publishedPort{host: host, protocol: protocol})
			}
		case yaml.MappingNode:
			pub := mappingValue(n, "published")
			if pub == nil || pub.Value == "" {
				continue
			}
			protocol := "tcp"
			if p := mappingValue(n, "protocol"); p != nil && p.Value != "" {
				protocol = p.Value
			}
			out = append(out, publishedPort{host: pub.Value, protocol: protocol})
		}
	}
	return out
}

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = strconv.Quote(s)
	}
	return strings.Join(quoted, ", ")
}
