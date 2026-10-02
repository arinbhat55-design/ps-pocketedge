package compose

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/template"
	"gopkg.in/yaml.v3"
)

// BuiltImagePrefix starts the tag of every image the platform builds from
// a service's build: section (see BuildTag).
const BuiltImagePrefix = "pspe-build/"

// supportedBuildKeys are the build: fields applied when building on an
// agent. Anything else is ignored, with a warning from Validate.
var supportedBuildKeys = map[string]bool{
	"context":    true,
	"dockerfile": true,
	"target":     true,
	"args":       true,
	"labels":     true,
	"no_cache":   true,
}

// BuildSpec is one service's build: section, resolved against the
// repository: Context is relative to the repository root (not to the
// Compose file, as written), Dockerfile relative to Context.
type BuildSpec struct {
	Service    string            `json:"service"`
	Context    string            `json:"context"`
	Dockerfile string            `json:"dockerfile"`
	Target     string            `json:"target,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	NoCache    bool              `json:"noCache,omitempty"`
}

// HasBuild reports whether any service in content has a build: section.
func HasBuild(content string) bool {
	services, err := servicesNode(content)
	if err != nil || services == nil {
		return false
	}
	for i := 0; i+1 < len(services.Content); i += 2 {
		if mappingValue(services.Content[i+1], "build") != nil {
			return true
		}
	}
	return false
}

// BuildSpecs returns the build: section of every service that has one, in
// file order. composeDir is the Compose file's directory in the repository
// (a build context is relative to it); env interpolates ${VAR} references
// in the build settings, as the deploy does for the rest of the file.
func BuildSpecs(content, composeDir string, env map[string]string) ([]BuildSpec, error) {
	services, err := servicesNode(content)
	if err != nil {
		return nil, err
	}
	if services == nil {
		return nil, nil
	}
	mapping := func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
	var specs []BuildSpec
	for i := 0; i+1 < len(services.Content); i += 2 {
		name := services.Content[i].Value
		node := mappingValue(services.Content[i+1], "build")
		if node == nil {
			continue
		}
		spec, err := parseBuild(name, node, composeDir, mapping)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", name, err)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func parseBuild(service string, node *yaml.Node, composeDir string, mapping template.Mapping) (BuildSpec, error) {
	spec := BuildSpec{Service: service, Dockerfile: "Dockerfile"}
	sub := func(field, s string) (string, error) {
		out, err := template.Substitute(s, mapping)
		if err != nil {
			return "", fmt.Errorf("build %s: %w", field, err)
		}
		return strings.TrimSpace(out), nil
	}

	var context string
	switch node.Kind {
	case yaml.ScalarNode:
		context = node.Value
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			var err error
			switch key {
			case "context":
				context = value.Value
			case "dockerfile":
				if spec.Dockerfile, err = sub("dockerfile", value.Value); err != nil {
					return spec, err
				}
			case "dockerfile_inline":
				return spec, errors.New("build dockerfile_inline isn't supported — commit the Dockerfile to the repository")
			case "target":
				if spec.Target, err = sub("target", value.Value); err != nil {
					return spec, err
				}
			case "no_cache":
				spec.NoCache = value.Value == "true"
			case "args", "labels":
				values, err := keyValues(value, mapping)
				if err != nil {
					return spec, fmt.Errorf("build %s: %w", key, err)
				}
				if key == "args" {
					spec.Args = values
				} else {
					spec.Labels = values
				}
			default:
				return spec, fmt.Errorf("build %s isn't supported", key)
			}
		}
	default:
		return spec, errors.New("build must be a path or a mapping")
	}

	context, err := sub("context", context)
	if err != nil {
		return spec, err
	}
	if context == "" {
		context = "."
	}
	if strings.Contains(context, "://") || strings.HasPrefix(context, "git@") {
		return spec, errors.New("remote build contexts aren't supported — the context must be a directory in the linked repository")
	}
	if path.IsAbs(context) {
		return spec, fmt.Errorf("build context %q must be relative to the Compose file", context)
	}
	spec.Context = path.Clean(path.Join(composeDir, context))
	if escapes(spec.Context) {
		return spec, fmt.Errorf("build context %q is outside the repository", context)
	}
	if spec.Dockerfile == "" {
		spec.Dockerfile = "Dockerfile"
	}
	if path.IsAbs(spec.Dockerfile) {
		return spec, fmt.Errorf("dockerfile %q must be relative to the build context", spec.Dockerfile)
	}
	spec.Dockerfile = path.Clean(spec.Dockerfile)
	if escapes(spec.Dockerfile) {
		return spec, fmt.Errorf("dockerfile %q must be inside the build context", spec.Dockerfile)
	}
	return spec, nil
}

// keyValues reads a build args/labels section: a mapping, or a list of
// KEY=value (or bare KEY, taking its value from the environment — Compose
// leaves an arg with no value unset).
func keyValues(node *yaml.Node, mapping template.Mapping) (map[string]string, error) {
	out := map[string]string{}
	set := func(key, value string, hasValue bool) error {
		if !hasValue {
			if v, ok := mapping(key); ok {
				out[key] = v
			}
			return nil
		}
		v, err := template.Substitute(value, mapping)
		if err != nil {
			return err
		}
		out[key] = v
		return nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			value := node.Content[i+1]
			if err := set(node.Content[i].Value, value.Value, value.Tag != "!!null"); err != nil {
				return nil, err
			}
		}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			key, value, found := strings.Cut(item.Value, "=")
			if err := set(key, value, found); err != nil {
				return nil, err
			}
		}
	default:
		return nil, errors.New("must be a mapping or a list")
	}
	return out, nil
}

func escapes(p string) bool {
	return p == ".." || strings.HasPrefix(p, "../")
}

// BuildTag is the tag a service's image is built as. It's derived from the
// repository, the commit, and every build setting, so the same inputs
// always give the same tag: an agent that already has the tag already has
// exactly this build and skips it, which is what makes redeploying,
// changing only runtime configuration, or rolling back instant.
func BuildTag(repositoryID, commit string, spec BuildSpec) string {
	repo := strings.ReplaceAll(repositoryID, "-", "")
	if len(repo) > 8 {
		repo = repo[:8]
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return fmt.Sprintf("%s%s-%s:%s-%s", BuiltImagePrefix, repo, imageNameComponent(spec.Service), commit, SettingsKey(spec)[:10])
}

// SettingsKey identifies a build's settings — everything it's built from
// except the code itself. The agent combines it with the Git tree of the
// build context, so a new commit that leaves the context untouched (only
// the Compose file changed, say) reuses the image already built.
func SettingsKey(spec BuildSpec) string {
	h := sha256.New()
	fmt.Fprintf(h, "context=%s\ndockerfile=%s\ntarget=%s\n", spec.Context, spec.Dockerfile, spec.Target)
	for _, section := range []struct {
		name   string
		values map[string]string
	}{{"arg", spec.Args}, {"label", spec.Labels}} {
		for _, k := range sortedKeys(section.values) {
			fmt.Fprintf(h, "%s:%s=%s\n", section.name, k, section.values[k])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// IsBuiltImage reports whether ref is an image the platform built.
func IsBuiltImage(ref string) bool {
	return strings.HasPrefix(ref, BuiltImagePrefix)
}

// imageNameComponent turns a service name into a valid image name
// component: lowercase alphanumerics separated by single dashes.
func imageNameComponent(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimSuffix(b.String(), "-")
	if out == "" {
		return "service"
	}
	return out
}

// PinBuiltImages replaces the build: section of each service in tags with
// image: <its tag>, giving Compose content the agent can deploy as-is.
func PinBuiltImages(content string, tags map[string]string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", err
	}
	if len(doc.Content) == 0 {
		return content, nil
	}
	services := mappingValue(doc.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return content, nil
	}
	for i := 0; i+1 < len(services.Content); i += 2 {
		tag, ok := tags[services.Content[i].Value]
		svc := services.Content[i+1]
		if !ok || svc.Kind != yaml.MappingNode {
			continue
		}
		var kept []*yaml.Node
		imageSet := false
		for j := 0; j+1 < len(svc.Content); j += 2 {
			key, value := svc.Content[j], svc.Content[j+1]
			switch key.Value {
			case "build", "pull_policy":
				continue
			case "image":
				value = &yaml.Node{Kind: yaml.ScalarNode, Value: tag}
				imageSet = true
			}
			kept = append(kept, key, value)
		}
		if !imageSet {
			kept = append([]*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "image"},
				{Kind: yaml.ScalarNode, Value: tag},
			}, kept...)
		}
		svc.Content = kept
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// DescribeChanges summarizes how newContent's configuration differs from
// oldContent's, per service and per top-level section, e.g.
// `service "api": ports, environment (+API_KEY)` or `service "worker"
// added`. Environment values are never included — only variable names.
func DescribeChanges(oldContent, newContent string) []string {
	oldDoc, newDoc := topLevel(oldContent), topLevel(newContent)
	oldServices, newServices := mappingValue(oldDoc, "services"), mappingValue(newDoc, "services")

	var changes []string
	for _, name := range unionKeys(oldServices, newServices) {
		o, n := mappingValue(oldServices, name), mappingValue(newServices, name)
		switch {
		case o == nil:
			changes = append(changes, fmt.Sprintf("service %q added", name))
		case n == nil:
			changes = append(changes, fmt.Sprintf("service %q removed", name))
		default:
			var fields []string
			for _, key := range unionKeys(o, n) {
				ov, nv := mappingValue(o, key), mappingValue(n, key)
				if nodeString(ov) == nodeString(nv) {
					continue
				}
				if key == "environment" {
					oldEnv, _ := parseEnvironmentOrEmpty(ov)
					newEnv, _ := parseEnvironmentOrEmpty(nv)
					if d := DescribeEnvChanges(oldEnv, newEnv); d != "" {
						key += " (" + d + ")"
					}
				}
				fields = append(fields, key)
			}
			if len(fields) > 0 {
				changes = append(changes, fmt.Sprintf("service %q: %s", name, strings.Join(fields, ", ")))
			}
		}
	}
	for _, key := range unionKeys(oldDoc, newDoc) {
		if key == "services" {
			continue
		}
		if nodeString(mappingValue(oldDoc, key)) != nodeString(mappingValue(newDoc, key)) {
			changes = append(changes, "top-level "+key)
		}
	}
	return changes
}

// DescribeEnvChanges lists variables added (+), removed (-) and changed
// (~) between two environments, by name only.
func DescribeEnvChanges(oldEnv, newEnv map[string]string) string {
	var parts []string
	for _, k := range sortedKeys(newEnv) {
		if ov, ok := oldEnv[k]; !ok {
			parts = append(parts, "+"+k)
		} else if ov != newEnv[k] {
			parts = append(parts, "~"+k)
		}
	}
	for _, k := range sortedKeys(oldEnv) {
		if _, ok := newEnv[k]; !ok {
			parts = append(parts, "-"+k)
		}
	}
	return strings.Join(parts, ", ")
}

func parseEnvironmentOrEmpty(node *yaml.Node) (map[string]string, bool) {
	if node == nil {
		return map[string]string{}, true
	}
	return parseEnvironment(node)
}

func servicesNode(content string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	services := mappingValue(doc.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, nil
	}
	return services, nil
}

func topLevel(content string) *yaml.Node {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}

func unionKeys(a, b *yaml.Node) []string {
	seen := map[string]bool{}
	var keys []string
	for _, n := range []*yaml.Node{a, b} {
		for _, k := range mappingKeys(n) {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// nodeString renders a node for comparison, ignoring comments and style.
func nodeString(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return n.Value
	}
	out, err := yaml.Marshal(v)
	if err != nil {
		return n.Value
	}
	return string(out)
}
