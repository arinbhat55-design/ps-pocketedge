// Package compose parses and renders the small subset of the Docker
// Compose file format that the Flutter dashboard's visual editor
// understands: a top-level services mapping where each service is limited
// to image, command, ports, environment, volumes, restart, and a
// deploy.resources CPU/memory limit. Parse is deliberately permissive
// about the rest of the Compose spec (build contexts, networks,
// healthchecks, long-form volumes, ...) — those files are still valid and
// listable, just flagged !VisualEditable so the dashboard falls back to
// YAML-only editing rather than silently dropping settings it doesn't
// model.
package compose

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/go-units"
	"gopkg.in/yaml.v3"
)

// knownServiceKeys are the Compose service fields the visual editor can
// represent. Any other key on a service marks the file !VisualEditable.
var knownServiceKeys = map[string]bool{
	"image":       true,
	"command":     true,
	"ports":       true,
	"environment": true,
	"volumes":     true,
	"restart":     true,
	"deploy":      true,
	"healthcheck": true,
}

// knownTopLevelKeys are the top-level Compose keys the visual editor
// tolerates. "volumes" is only tolerated when every entry is an empty
// named-volume declaration (see isPlainVolumeDeclaration) — anything
// fancier (drivers, external volumes, networks, secrets, ...) marks the
// file !VisualEditable.
var knownTopLevelKeys = map[string]bool{
	"version":  true,
	"services": true,
	"volumes":  true,
}

// ServiceDraft is the visual editor's simplified view of one Compose
// service — a lossy projection of the full spec, used both as the output
// of Parse (to hydrate the visual editor from existing YAML) and the input
// to Render (to generate YAML from the visual editor's form state).
type ServiceDraft struct {
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Command     string            `json:"command,omitempty"`
	Ports       []string          `json:"ports,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Volumes     []string          `json:"volumes,omitempty"`
	Restart     string            `json:"restart,omitempty"`
	// NanoCPUs/MemoryLimitBytes/MemoryReservationBytes mirror
	// deploy.resources.limits/reservations — same units the standalone
	// container resource-limit fields already use (UpdateResourceLimits),
	// so a Compose-declared limit behaves identically to one set
	// afterward via the Containers UI. 0 means unset/unlimited.
	NanoCPUs               int64 `json:"nanoCpus,omitempty"`
	MemoryLimitBytes       int64 `json:"memoryLimitBytes,omitempty"`
	MemoryReservationBytes int64 `json:"memoryReservationBytes,omitempty"`
	// HealthCheckTest is the shell command run via CMD-SHELL; empty means
	// no healthcheck declared. The other fields are ignored when it's
	// empty. Interval/Timeout/StartPeriod are in seconds, matching how
	// the visual editor displays them (Compose itself uses Go-style
	// duration strings like "30s").
	HealthCheckTest               string `json:"healthCheckTest,omitempty"`
	HealthCheckIntervalSeconds    int64  `json:"healthCheckIntervalSeconds,omitempty"`
	HealthCheckTimeoutSeconds     int64  `json:"healthCheckTimeoutSeconds,omitempty"`
	HealthCheckRetries            int64  `json:"healthCheckRetries,omitempty"`
	HealthCheckStartPeriodSeconds int64  `json:"healthCheckStartPeriodSeconds,omitempty"`
}

// ParseResult is what Parse reports about one Compose file.
type ParseResult struct {
	// Valid is false only for structural problems that would keep the
	// file from being saved at all: invalid YAML syntax, a missing or
	// empty "services" section, or a service that isn't a mapping.
	Valid bool `json:"valid"`
	// Errors explains every Valid=false condition found; empty when Valid.
	Errors []string `json:"errors"`
	// ServiceNames lists every service found, in file order, regardless
	// of VisualEditable.
	ServiceNames []string `json:"serviceNames"`
	// VisualEditable is true when the whole file — top level and every
	// service — uses only the fields ServiceDraft can represent, meaning
	// it's safe to open in the visual editor without losing settings on
	// save.
	VisualEditable bool `json:"visualEditable"`
	// Services holds a best-effort ServiceDraft per service; populated
	// even when VisualEditable is false, but only meaningful for
	// individual services that were themselves fully representable.
	Services []ServiceDraft `json:"services,omitempty"`
}

// Parse validates content as a Compose file and, best-effort, projects it
// into ServiceDrafts for the visual editor.
func Parse(content string) ParseResult {
	if strings.TrimSpace(content) == "" {
		return ParseResult{Valid: false, Errors: []string{"compose file is empty"}}
	}

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return ParseResult{Valid: false, Errors: []string{"invalid YAML: " + err.Error()}}
	}
	if len(root.Content) == 0 {
		return ParseResult{Valid: false, Errors: []string{"compose file is empty"}}
	}

	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return ParseResult{Valid: false, Errors: []string{"top-level document must be a mapping, e.g. 'services: ...'"}}
	}

	visualEditable := true
	var servicesNode *yaml.Node
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i].Value
		if !knownTopLevelKeys[key] {
			visualEditable = false
		}
		switch key {
		case "services":
			servicesNode = doc.Content[i+1]
		case "volumes":
			if !isPlainVolumeSection(doc.Content[i+1]) {
				visualEditable = false
			}
		}
	}

	if servicesNode == nil {
		return ParseResult{Valid: false, Errors: []string{"compose file must define a top-level 'services' section"}}
	}
	if servicesNode.Kind != yaml.MappingNode || len(servicesNode.Content) == 0 {
		return ParseResult{Valid: false, Errors: []string{"'services' must be a non-empty mapping of service name to definition"}}
	}

	var names []string
	var drafts []ServiceDraft
	var errs []string
	for i := 0; i+1 < len(servicesNode.Content); i += 2 {
		name := servicesNode.Content[i].Value
		names = append(names, name)
		valueNode := servicesNode.Content[i+1]
		if valueNode.Kind != yaml.MappingNode {
			errs = append(errs, fmt.Sprintf("service %q must be a mapping", name))
			visualEditable = false
			continue
		}
		draft, editable := parseService(name, valueNode)
		drafts = append(drafts, draft)
		if !editable {
			visualEditable = false
		}
	}

	if len(errs) > 0 {
		return ParseResult{Valid: false, Errors: errs, ServiceNames: names}
	}

	return ParseResult{
		Valid:          true,
		ServiceNames:   names,
		VisualEditable: visualEditable,
		Services:       drafts,
	}
}

// parseService projects one service mapping node into a ServiceDraft,
// reporting whether every key on it was representable.
func parseService(name string, node *yaml.Node) (ServiceDraft, bool) {
	draft := ServiceDraft{Name: name}
	editable := true

	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]
		if !knownServiceKeys[key] {
			editable = false
			continue
		}
		switch key {
		case "image":
			if val.Kind == yaml.ScalarNode {
				draft.Image = val.Value
			} else {
				editable = false
			}
		case "restart":
			if val.Kind == yaml.ScalarNode {
				draft.Restart = val.Value
			} else {
				editable = false
			}
		case "command":
			cmd, ok := scalarOrJoinedSequence(val)
			if !ok {
				editable = false
			}
			draft.Command = cmd
		case "ports":
			ports, ok := stringSequence(val)
			if !ok {
				editable = false
			}
			draft.Ports = ports
		case "volumes":
			vols, ok := stringSequence(val)
			if !ok {
				editable = false
			}
			draft.Volumes = vols
		case "environment":
			env, ok := parseEnvironment(val)
			if !ok {
				editable = false
			}
			draft.Environment = env
		case "deploy":
			nanoCPUs, memLimit, memReservation, ok := parseDeployResources(val)
			if !ok {
				editable = false
			}
			draft.NanoCPUs = nanoCPUs
			draft.MemoryLimitBytes = memLimit
			draft.MemoryReservationBytes = memReservation
		case "healthcheck":
			hc, ok := parseHealthCheck(val)
			if !ok {
				editable = false
			}
			draft.HealthCheckTest = hc.test
			draft.HealthCheckIntervalSeconds = hc.intervalSeconds
			draft.HealthCheckTimeoutSeconds = hc.timeoutSeconds
			draft.HealthCheckRetries = hc.retries
			draft.HealthCheckStartPeriodSeconds = hc.startPeriodSeconds
		}
	}

	return draft, editable
}

// parseDeployResources reads deploy.resources.limits/reservations
// cpus+memory out of a service's "deploy" mapping node. Returns ok=false
// if deploy contains anything else (replicas, restart_policy, placement,
// ...) that the visual editor can't represent — the resource values
// gathered before that point are still returned best-effort.
func parseDeployResources(node *yaml.Node) (nanoCPUs, memLimit, memReservation int64, ok bool) {
	if node.Kind != yaml.MappingNode {
		return 0, 0, 0, false
	}
	ok = true
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]
		if key != "resources" {
			ok = false
			continue
		}
		if val.Kind != yaml.MappingNode {
			ok = false
			continue
		}
		for j := 0; j+1 < len(val.Content); j += 2 {
			resourceKey := val.Content[j].Value
			resourceVal := val.Content[j+1]
			cpus, mem, rok := parseResourceEntry(resourceVal)
			if !rok {
				ok = false
			}
			switch resourceKey {
			case "limits":
				nanoCPUs, memLimit = cpus, mem
			case "reservations":
				memReservation = mem
			default:
				ok = false
			}
		}
	}
	return nanoCPUs, memLimit, memReservation, ok
}

// parseResourceEntry reads cpus/memory out of one deploy.resources.limits
// or .reservations mapping node.
func parseResourceEntry(node *yaml.Node) (nanoCPUs, memBytes int64, ok bool) {
	if node.Kind != yaml.MappingNode {
		return 0, 0, false
	}
	ok = true
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]
		if val.Kind != yaml.ScalarNode {
			ok = false
			continue
		}
		switch key {
		case "cpus":
			cores, err := strconv.ParseFloat(val.Value, 64)
			if err != nil {
				ok = false
				continue
			}
			nanoCPUs = int64(cores * 1e9)
		case "memory":
			b, err := units.RAMInBytes(val.Value)
			if err != nil {
				ok = false
				continue
			}
			memBytes = b
		default:
			ok = false
		}
	}
	return nanoCPUs, memBytes, ok
}

// healthCheckFields is parseHealthCheck's return value, grouped into a
// struct since it's five values wide.
type healthCheckFields struct {
	test               string
	intervalSeconds    int64
	timeoutSeconds     int64
	retries            int64
	startPeriodSeconds int64
}

// parseHealthCheck reads a service's "healthcheck" mapping node. Only the
// CMD-SHELL and NONE test forms are representable (a plain string implies
// CMD-SHELL); the exec form (["CMD", "curl", ...]) has different
// shell-vs-no-shell semantics that a single command string can't safely
// preserve, so it marks the service not visual-editable instead of
// silently changing behavior.
func parseHealthCheck(node *yaml.Node) (healthCheckFields, bool) {
	var fields healthCheckFields
	if node.Kind != yaml.MappingNode {
		return fields, false
	}
	ok := true
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]
		switch key {
		case "test":
			test, tok := parseHealthCheckTest(val)
			if !tok {
				ok = false
			}
			fields.test = test
		case "interval":
			d, dok := parseDurationScalar(val)
			if !dok {
				ok = false
			}
			fields.intervalSeconds = d
		case "timeout":
			d, dok := parseDurationScalar(val)
			if !dok {
				ok = false
			}
			fields.timeoutSeconds = d
		case "start_period":
			d, dok := parseDurationScalar(val)
			if !dok {
				ok = false
			}
			fields.startPeriodSeconds = d
		case "retries":
			if val.Kind != yaml.ScalarNode {
				ok = false
				continue
			}
			n, err := strconv.ParseInt(val.Value, 10, 64)
			if err != nil {
				ok = false
				continue
			}
			fields.retries = n
		default:
			ok = false
		}
	}
	return fields, ok
}

// parseHealthCheckTest reads a healthcheck's "test" field: a bare scalar
// (implicit CMD-SHELL), ["CMD-SHELL", "cmd"], or ["NONE"] (healthcheck
// explicitly disabled, represented here as an empty test string — the
// same as "no healthcheck configured").
func parseHealthCheckTest(node *yaml.Node) (string, bool) {
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Value, true
	case yaml.SequenceNode:
		if len(node.Content) == 1 && node.Content[0].Value == "NONE" {
			return "", true
		}
		if len(node.Content) == 2 && node.Content[0].Value == "CMD-SHELL" {
			return node.Content[1].Value, true
		}
		return "", false
	default:
		return "", false
	}
}

// parseDurationScalar reads a Go-style duration string ("30s", "1m30s") —
// the form Compose's healthcheck interval/timeout/start_period fields use.
func parseDurationScalar(node *yaml.Node) (int64, bool) {
	if node.Kind != yaml.ScalarNode {
		return 0, false
	}
	d, err := time.ParseDuration(node.Value)
	if err != nil {
		return 0, false
	}
	return int64(d.Seconds()), true
}

// scalarOrJoinedSequence handles Compose's shell-form ("a b c") vs exec-form
// ([a, b, c]) command syntax, collapsing either into a single string.
func scalarOrJoinedSequence(node *yaml.Node) (string, bool) {
	switch node.Kind {
	case yaml.ScalarNode:
		return node.Value, true
	case yaml.SequenceNode:
		parts := make([]string, 0, len(node.Content))
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				return "", false
			}
			parts = append(parts, item.Value)
		}
		return strings.Join(parts, " "), true
	default:
		return "", false
	}
}

// stringSequence reads a YAML sequence of plain scalars (used for ports and
// volumes' short syntax). Returns ok=false if any entry isn't a scalar
// (e.g. the long map-form syntax), in which case the caller should treat
// the containing service as not visual-editable.
func stringSequence(node *yaml.Node) ([]string, bool) {
	if node.Kind != yaml.SequenceNode {
		return nil, false
	}
	out := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			continue
		}
		out = append(out, item.Value)
	}
	return out, len(out) == len(node.Content)
}

// parseEnvironment accepts both Compose environment forms: a mapping of
// KEY: value, or a sequence of "KEY=value" strings. A mapping entry with no
// value (null, meaning "inherit from the shell running compose") isn't
// representable in the visual editor's plain key/value rows, so it marks
// the service not editable.
func parseEnvironment(node *yaml.Node) (map[string]string, bool) {
	env := map[string]string{}
	switch node.Kind {
	case yaml.MappingNode:
		ok := true
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			val := node.Content[i+1]
			if val.Kind != yaml.ScalarNode || val.Tag == "!!null" {
				ok = false
				continue
			}
			env[key] = val.Value
		}
		return env, ok
	case yaml.SequenceNode:
		ok := true
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				ok = false
				continue
			}
			key, value, found := strings.Cut(item.Value, "=")
			if !found {
				ok = false
				continue
			}
			env[key] = value
		}
		return env, ok
	default:
		return env, false
	}
}

// isPlainVolumeSection reports whether a top-level "volumes:" mapping only
// declares bare named volumes (no driver, external, or labels config) —
// the shape Render itself produces, so a file using only this form can
// round-trip through the visual editor.
func isPlainVolumeSection(node *yaml.Node) bool {
	if node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		val := node.Content[i+1]
		switch val.Kind {
		case 0:
			continue // null, e.g. "myvol:"
		case yaml.MappingNode:
			if len(val.Content) != 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isNamedVolumeSource reports whether a Compose "volumes" short-syntax
// mount source refers to a named volume rather than a bind-mounted host
// path — i.e. everything except an absolute or relative filesystem path.
func isNamedVolumeSource(source string) bool {
	if source == "" {
		return false
	}
	return !strings.HasPrefix(source, "/") && !strings.HasPrefix(source, ".") && !strings.HasPrefix(source, "~")
}

var errInvalidServiceName = errors.New("service names must contain only letters, digits, '.', '_', and '-'")

func validServiceName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// Render generates Compose YAML from the visual editor's service drafts.
// Output is hand-built rather than produced via yaml.Marshal on a
// map[string]ServiceDraft so that service and top-level key order stays
// exactly what the caller passed in — Go map iteration order is randomized,
// which would otherwise make every render reshuffle the file.
func Render(services []ServiceDraft) (string, error) {
	if len(services) == 0 {
		return "", errors.New("at least one service is required")
	}

	var b strings.Builder
	namedVolumes := map[string]bool{}
	var namedVolumeOrder []string
	seenNames := map[string]bool{}

	b.WriteString("services:\n")
	for _, svc := range services {
		if !validServiceName(svc.Name) {
			return "", fmt.Errorf("%q: %w", svc.Name, errInvalidServiceName)
		}
		if seenNames[svc.Name] {
			return "", fmt.Errorf("duplicate service name %q", svc.Name)
		}
		seenNames[svc.Name] = true
		if strings.TrimSpace(svc.Image) == "" {
			return "", fmt.Errorf("service %q: image is required", svc.Name)
		}
		fmt.Fprintf(&b, "  %s:\n", yamlKey(svc.Name))
		fmt.Fprintf(&b, "    image: %s\n", yamlQuote(svc.Image))
		if strings.TrimSpace(svc.Command) != "" {
			fmt.Fprintf(&b, "    command: %s\n", yamlQuote(svc.Command))
		}
		if len(svc.Ports) > 0 {
			b.WriteString("    ports:\n")
			for _, p := range svc.Ports {
				if strings.TrimSpace(p) == "" {
					continue
				}
				fmt.Fprintf(&b, "      - %s\n", yamlQuote(p))
			}
		}
		if len(svc.Environment) > 0 {
			b.WriteString("    environment:\n")
			for _, key := range sortedKeys(svc.Environment) {
				fmt.Fprintf(&b, "      %s: %s\n", yamlKey(key), yamlQuote(svc.Environment[key]))
			}
		}
		if len(svc.Volumes) > 0 {
			b.WriteString("    volumes:\n")
			for _, v := range svc.Volumes {
				if strings.TrimSpace(v) == "" {
					continue
				}
				fmt.Fprintf(&b, "      - %s\n", yamlQuote(v))
				source, _, found := strings.Cut(v, ":")
				if found && isNamedVolumeSource(source) && !namedVolumes[source] {
					namedVolumes[source] = true
					namedVolumeOrder = append(namedVolumeOrder, source)
				}
			}
		}
		if strings.TrimSpace(svc.Restart) != "" {
			fmt.Fprintf(&b, "    restart: %s\n", yamlQuote(svc.Restart))
		}
		if svc.NanoCPUs > 0 || svc.MemoryLimitBytes > 0 || svc.MemoryReservationBytes > 0 {
			b.WriteString("    deploy:\n      resources:\n")
			if svc.NanoCPUs > 0 || svc.MemoryLimitBytes > 0 {
				b.WriteString("        limits:\n")
				if svc.NanoCPUs > 0 {
					fmt.Fprintf(&b, "          cpus: %s\n", yamlQuote(formatCPUs(svc.NanoCPUs)))
				}
				if svc.MemoryLimitBytes > 0 {
					fmt.Fprintf(&b, "          memory: %s\n", yamlQuote(strconv.FormatInt(svc.MemoryLimitBytes, 10)))
				}
			}
			if svc.MemoryReservationBytes > 0 {
				b.WriteString("        reservations:\n")
				fmt.Fprintf(&b, "          memory: %s\n", yamlQuote(strconv.FormatInt(svc.MemoryReservationBytes, 10)))
			}
		}
		if strings.TrimSpace(svc.HealthCheckTest) != "" {
			b.WriteString("    healthcheck:\n")
			fmt.Fprintf(&b, "      test: [%s, %s]\n", yamlQuote("CMD-SHELL"), yamlQuote(svc.HealthCheckTest))
			if svc.HealthCheckIntervalSeconds > 0 {
				fmt.Fprintf(&b, "      interval: %s\n", yamlQuote(formatSeconds(svc.HealthCheckIntervalSeconds)))
			}
			if svc.HealthCheckTimeoutSeconds > 0 {
				fmt.Fprintf(&b, "      timeout: %s\n", yamlQuote(formatSeconds(svc.HealthCheckTimeoutSeconds)))
			}
			if svc.HealthCheckStartPeriodSeconds > 0 {
				fmt.Fprintf(&b, "      start_period: %s\n", yamlQuote(formatSeconds(svc.HealthCheckStartPeriodSeconds)))
			}
			if svc.HealthCheckRetries > 0 {
				fmt.Fprintf(&b, "      retries: %d\n", svc.HealthCheckRetries)
			}
		}
	}

	if len(namedVolumeOrder) > 0 {
		b.WriteString("\nvolumes:\n")
		for _, name := range namedVolumeOrder {
			fmt.Fprintf(&b, "  %s: {}\n", yamlKey(name))
		}
	}

	return b.String(), nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// yamlKey quotes a mapping key only when it's not a plain bareword, to keep
// rendered output readable for the common case.
func yamlKey(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return yamlQuote(s)
		}
	}
	return s
}

// yamlQuote renders s as a double-quoted YAML scalar. Always quoting (never
// emitting a bareword) sidesteps every ambiguity a hand-written emitter
// would otherwise have to special-case — numbers, booleans, colons,
// leading dashes — at the cost of slightly noisier output.
func yamlQuote(s string) string {
	return strconv.Quote(s)
}

// formatCPUs renders Docker's nanoCPUs (cores * 1e9) back into the plain
// core-count string Compose's deploy.resources.limits.cpus expects, e.g.
// 1500000000 -> "1.5".
func formatCPUs(nanoCPUs int64) string {
	return strconv.FormatFloat(float64(nanoCPUs)/1e9, 'f', -1, 64)
}

// formatSeconds renders a second count as the Go-style duration string
// Compose's healthcheck interval/timeout/start_period fields expect.
func formatSeconds(seconds int64) string {
	return (time.Duration(seconds) * time.Second).String()
}
