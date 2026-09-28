// Package dbcatalog is the Database Marketplace's curated engine catalog:
// what each engine is, which versions are offered, and how to turn the
// deployment wizard's choices into an operationally ready Compose file —
// persistent storage, a health check, resource limits and engine memory
// tuning, loopback-only ports unless remote access is asked for, and
// credentials that never appear in the Compose content itself.
//
// Rendering never generates or sees a credential value. A Plan lists the
// secrets it needs (SecretSpec) and refers to them only as ${VAR}
// interpolations; the caller generates each value, seals it in the vault,
// and puts a vault reference in the deployment's env under that VAR.
//
// Engines also describe how to operate on a running instance — rotating
// the admin password and issuing or dropping temporary users — as argv
// commands run inside the container (see Commands). An engine that can't
// do one of those safely says so (RotationNone, TemporaryUsers false)
// rather than pretending to.
package dbcatalog

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Category groups engines in the marketplace.
type Category string

const (
	CategoryRelational Category = "relational"
	CategoryNoSQL      Category = "nosql"
	CategoryCache      Category = "cache"
	CategoryAnalytics  Category = "analytics"
	CategoryVector     Category = "vector"
)

// AuthMode is how clients authenticate to an engine as deployed here.
type AuthMode string

const (
	// AuthPassword: a username and generated password.
	AuthPassword AuthMode = "password"
	// AuthToken: a generated API key, no username.
	AuthToken AuthMode = "token"
	// AuthNone: the engine's image offers no way to switch authentication
	// on from Compose alone. The wizard defaults these to local-only
	// access and warns before exposing them.
	AuthNone AuthMode = "none"
)

// Rotation is how an engine's admin credential is rotated.
type Rotation string

const (
	// RotationExec runs a command in the running container that changes
	// the password in place (ALTER ROLE, changeUserPassword, ...). No
	// restart.
	RotationExec Rotation = "exec"
	// RotationRedeploy: the engine reads its password from the
	// environment every time it starts, so rotating means storing the new
	// value and recreating the containers. Causes a brief restart.
	RotationRedeploy Rotation = "redeploy"
	// RotationNone: the engine has no credential to rotate.
	RotationNone Rotation = "none"
)

// Version is one offered image tag.
type Version struct {
	Tag   string `json:"tag"`
	Label string `json:"label"`
	// dataPath overrides the engine's data directory for this version
	// (PostgreSQL 18 moved it).
	dataPath string
}

// Port is a container port an engine listens on.
type Port struct {
	Container int    `json:"container"`
	Name      string `json:"name"`
}

// Engine is one catalog entry. Exported fields are what the marketplace
// UI shows; the unexported build/commands implement it.
type Engine struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Category    Category  `json:"category"`
	Description string    `json:"description"`
	Image       string    `json:"image"`
	Versions    []Version `json:"versions"`
	// Port is the primary client port; ExtraPorts (admin UI, gRPC, native
	// protocol) are published with the same access binding, shifted by
	// the same offset as the chosen primary port (see ExtraHostPorts), so
	// two instances of one engine on a server don't collide.
	Port       Port     `json:"port"`
	ExtraPorts []Port   `json:"extraPorts,omitempty"`
	Auth       AuthMode `json:"auth"`
	// FixedUsername is set when the engine's admin login can't be chosen
	// (SQL Server's "sa", Redis's "default"). UsernameAllowed is false
	// when there's no username at all.
	FixedUsername   string `json:"fixedUsername,omitempty"`
	UsernameAllowed bool   `json:"usernameAllowed"`
	DefaultUsername string `json:"defaultUsername,omitempty"`
	// DatabaseNameAllowed is whether the engine creates a named database
	// at first start; DatabaseNameLabel is what that name means for it
	// (a bucket for InfluxDB, a keyspace for Cassandra, ...).
	DatabaseNameAllowed bool   `json:"databaseNameAllowed"`
	DatabaseNameLabel   string `json:"databaseNameLabel,omitempty"`
	// Architectures the engine's images are published for, as Go GOARCH
	// values (matching servers.arch).
	Architectures []string `json:"architectures"`
	// Persistent is false for purely in-memory engines (no volume, no
	// backups).
	Persistent bool `json:"persistent"`
	// MinMemoryMB is the smallest memory limit the engine reliably starts
	// with; DefaultMemoryMB is the wizard's starting value.
	MinMemoryMB      int     `json:"minMemoryMb"`
	DefaultMemoryMB  int     `json:"defaultMemoryMb"`
	DefaultCPUs      float64 `json:"defaultCpus"`
	DefaultStorageGB int     `json:"defaultStorageGb"`
	// HighAvailability describes what the HA option deploys, empty if the
	// engine doesn't offer one.
	HighAvailability string   `json:"highAvailability,omitempty"`
	Rotation         Rotation `json:"rotation"`
	TemporaryUsers   bool     `json:"temporaryUsers"`
	// License is a short license summary. RequiresLicenseAcceptance means
	// the wizard must get explicit acceptance before deploying, and
	// Editions lists the edition choices (SQL Server).
	License                   string   `json:"license"`
	RequiresLicenseAcceptance bool     `json:"requiresLicenseAcceptance,omitempty"`
	Editions                  []string `json:"editions,omitempty"`
	Notes                     []string `json:"notes,omitempty"`
	// ConnectionScheme drives ConnectionString.
	ConnectionScheme string `json:"connectionScheme"`

	dataPath string
	build    func(b *builder) error
	commands *commandSet
}

// Options are the wizard's choices.
type Options struct {
	Name             string
	Version          string
	DatabaseName     string
	Username         string
	Port             int
	Access           string // local | remote
	StorageGB        int
	MemoryMB         int
	CPUs             float64
	HighAvailability bool
	Profile          string // development | production
	Edition          string
	AcceptLicense    bool
}

// SecretSpec is a credential a Plan needs generated.
type SecretSpec struct {
	// EnvVar is the Compose interpolation variable the value is read
	// from.
	EnvVar string
	// Kind is "admin" (the primary login) or "token" (an additional key).
	Kind     string
	Name     string
	Username string
}

// Plan is a rendered deployment.
type Plan struct {
	ComposeYAML string
	// Env is the non-secret interpolation env. The caller adds a vault
	// reference for every SecretSpec.EnvVar.
	Env     map[string]string
	Secrets []SecretSpec
	// Service is the Compose service clients connect to, and the one
	// rotation / temporary-user commands run in.
	Service  string
	Username string
	Warnings []string
}

var (
	instanceNameRE     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,38}[a-z0-9]$`)
	identifierRE       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
	postgresMinorTagRE = regexp.MustCompile(`^([0-9]{2})\.([0-9]{1,3})$`)
)

// Get returns the engine with id.
func Get(id string) (*Engine, bool) {
	for i := range engines {
		if engines[i].ID == id {
			return &engines[i], true
		}
	}
	return nil, false
}

// All returns every engine, in catalog order.
func All() []Engine { return engines }

// DefaultVersion is the engine's recommended tag.
func (e *Engine) DefaultVersion() string { return e.Versions[0].Tag }

// SupportsArch reports whether the engine publishes images for arch. An
// unknown arch (older agent) is allowed through.
func (e *Engine) SupportsArch(arch string) bool {
	return arch == "" || slices.Contains(e.Architectures, arch)
}

func (e *Engine) version(tag string) (Version, bool) {
	for _, v := range e.Versions {
		if v.Tag == tag {
			return v, true
		}
	}
	if e.ID == "postgresql" {
		if match := postgresMinorTagRE.FindStringSubmatch(tag); match != nil {
			for _, offered := range e.Versions {
				if offered.Tag == match[1] {
					return Version{Tag: tag, Label: tag, dataPath: offered.dataPath}, true
				}
			}
		}
	}
	return Version{}, false
}

// Normalize fills defaults into opts and validates them against the
// engine. It returns the options to render with.
func (e *Engine) Normalize(opts Options) (Options, error) {
	if !instanceNameRE.MatchString(opts.Name) {
		return opts, fmt.Errorf("name must be 3-40 lowercase letters, digits or hyphens, starting with a letter")
	}
	if opts.Version == "" {
		opts.Version = e.DefaultVersion()
	}
	if _, ok := e.version(opts.Version); !ok {
		return opts, fmt.Errorf("version %q is not offered for %s", opts.Version, e.Name)
	}
	if opts.Profile == "" {
		opts.Profile = "development"
	}
	if opts.Profile != "development" && opts.Profile != "production" {
		return opts, fmt.Errorf("profile must be development or production")
	}
	if opts.Access == "" {
		opts.Access = "local"
	}
	if opts.Access != "local" && opts.Access != "remote" {
		return opts, fmt.Errorf("access must be local or remote")
	}
	if opts.Port == 0 {
		opts.Port = e.Port.Container
	}
	if opts.Port < 1024 || opts.Port > 65535 {
		return opts, fmt.Errorf("port must be between 1024 and 65535")
	}
	for _, p := range e.ExtraHostPorts(opts.Port) {
		if p.Host < 1024 || p.Host > 65535 {
			return opts, fmt.Errorf("with port %d, the %s port would be %d, outside 1024-65535; choose a different port", opts.Port, p.Name, p.Host)
		}
	}

	switch {
	case e.FixedUsername != "":
		opts.Username = e.FixedUsername
	case !e.UsernameAllowed:
		opts.Username = ""
	default:
		if opts.Username == "" {
			opts.Username = e.DefaultUsername
		}
		if !identifierRE.MatchString(opts.Username) {
			return opts, fmt.Errorf("administrator username must start with a letter or underscore and contain only letters, digits and underscores")
		}
		if e.commands != nil && (slices.Contains(e.commands.reservedUsernames, strings.ToLower(opts.Username)) ||
			(e.commands.reservedPrefix != "" && strings.HasPrefix(strings.ToLower(opts.Username), e.commands.reservedPrefix))) {
			return opts, fmt.Errorf("%q is reserved by %s; choose another administrator username", opts.Username, e.Name)
		}
	}

	if e.DatabaseNameAllowed {
		if opts.DatabaseName == "" {
			opts.DatabaseName = "app"
		}
		if !identifierRE.MatchString(opts.DatabaseName) {
			return opts, fmt.Errorf("%s must start with a letter or underscore and contain only letters, digits and underscores", strings.ToLower(e.DatabaseNameLabel))
		}
	} else {
		opts.DatabaseName = ""
	}

	if opts.MemoryMB == 0 {
		opts.MemoryMB = e.DefaultMemoryMB
	}
	if opts.MemoryMB < e.MinMemoryMB {
		return opts, fmt.Errorf("%s needs at least %d MB of memory", e.Name, e.MinMemoryMB)
	}
	if opts.CPUs == 0 {
		opts.CPUs = e.DefaultCPUs
	}
	if opts.CPUs < 0.25 || opts.CPUs > 64 {
		return opts, fmt.Errorf("CPU limit must be between 0.25 and 64")
	}
	if e.Persistent {
		if opts.StorageGB == 0 {
			opts.StorageGB = e.DefaultStorageGB
		}
		if opts.StorageGB < 1 || opts.StorageGB > 65536 {
			return opts, fmt.Errorf("storage must be between 1 and 65536 GB")
		}
	} else {
		opts.StorageGB = 0
	}
	if opts.HighAvailability && e.HighAvailability == "" {
		return opts, fmt.Errorf("%s does not offer a high-availability option", e.Name)
	}

	if e.RequiresLicenseAcceptance && !opts.AcceptLicense {
		return opts, fmt.Errorf("%s requires accepting its license terms", e.Name)
	}
	if len(e.Editions) > 0 {
		if opts.Edition == "" {
			opts.Edition = e.Editions[0]
		}
		if !slices.Contains(e.Editions, opts.Edition) {
			return opts, fmt.Errorf("edition must be one of %s", strings.Join(e.Editions, ", "))
		}
	}
	return opts, nil
}

// Render builds the Compose file for already-normalized options.
func (e *Engine) Render(opts Options) (*Plan, error) {
	v, _ := e.version(opts.Version)
	dataPath := e.dataPath
	if v.dataPath != "" {
		dataPath = v.dataPath
	}
	b := &builder{
		engine:   e,
		opts:     opts,
		image:    e.Image + ":" + opts.Version,
		dataPath: dataPath,
		file:     composeFile{Services: map[string]*service{}},
		plan:     &Plan{Env: map[string]string{}, Username: opts.Username},
	}
	if err := e.build(b); err != nil {
		return nil, err
	}
	if b.plan.Service == "" {
		return nil, fmt.Errorf("engine %s rendered no primary service", e.ID)
	}
	if opts.Access == "remote" && e.Auth == AuthNone {
		b.plan.Warnings = append(b.plan.Warnings, fmt.Sprintf("%s has no authentication in this configuration; remote access exposes it to anyone who can reach port %d", e.Name, opts.Port))
	}
	if e.Persistent {
		b.plan.Warnings = append(b.plan.Warnings, fmt.Sprintf("the %d GB storage allocation is checked against the server's disk and recorded, but Docker's local volume driver does not enforce a size limit", opts.StorageGB))
	}
	out, err := b.file.marshal()
	if err != nil {
		return nil, err
	}
	b.plan.ComposeYAML = out
	return b.plan, nil
}

// HostPort is an extra port's published host port.
type HostPort struct {
	Name      string `json:"name"`
	Container int    `json:"container"`
	Host      int    `json:"host"`
}

// ExtraHostPorts returns where each extra port is published for a given
// primary host port: shifted by the primary's offset from its default,
// so the default primary port keeps every port at its usual number.
func (e *Engine) ExtraHostPorts(primaryHostPort int) []HostPort {
	offset := primaryHostPort - e.Port.Container
	out := make([]HostPort, 0, len(e.ExtraPorts))
	for _, p := range e.ExtraPorts {
		out = append(out, HostPort{Name: p.Name, Container: p.Container, Host: p.Container + offset})
	}
	return out
}

// ConnectionString is a client URI for the instance with the password
// left as a placeholder — the real value only ever leaves the vault
// through the credential endpoints.
func (e *Engine) ConnectionString(host string, port int, username, database string) string {
	userinfo := ""
	switch e.Auth {
	case AuthPassword:
		userinfo = username + ":<password>@"
	}
	path := ""
	if database != "" {
		path = "/" + database
	}
	switch e.ConnectionScheme {
	case "sqlserver":
		return fmt.Sprintf("sqlserver://%s:<password>@%s:%d?encrypt=true&trustServerCertificate=true", username, host, port)
	case "mongodb":
		return fmt.Sprintf("mongodb://%s%s:%d%s?authSource=admin", userinfo, host, port, path)
	case "http", "https":
		return fmt.Sprintf("%s://%s:%d", e.ConnectionScheme, host, port)
	case "cassandra", "memcached", "milvus":
		return fmt.Sprintf("%s:%d", host, port)
	}
	return fmt.Sprintf("%s://%s%s:%d%s", e.ConnectionScheme, userinfo, host, port, path)
}
