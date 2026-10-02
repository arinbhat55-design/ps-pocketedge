package dbcatalog

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

// composeFile is the subset of the Compose spec the catalog renders. Field
// order here is the order keys appear in the rendered YAML.
type composeFile struct {
	Services map[string]*service  `yaml:"services"`
	Volumes  map[string]*struct{} `yaml:"volumes,omitempty"`
}

type service struct {
	Image       string               `yaml:"image"`
	Command     []string             `yaml:"command,omitempty"`
	Environment map[string]string    `yaml:"environment,omitempty"`
	Ports       []string             `yaml:"ports,omitempty"`
	Volumes     []string             `yaml:"volumes,omitempty"`
	DependsOn   map[string]dependsOn `yaml:"depends_on,omitempty"`
	Restart     string               `yaml:"restart"`
	Healthcheck *healthcheck         `yaml:"healthcheck,omitempty"`
	Deploy      *deploy              `yaml:"deploy,omitempty"`
	Labels      map[string]string    `yaml:"labels,omitempty"`
}

type dependsOn struct {
	Condition string `yaml:"condition"`
}

type healthcheck struct {
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval"`
	Timeout     string   `yaml:"timeout"`
	Retries     int      `yaml:"retries"`
	StartPeriod string   `yaml:"start_period"`
}

type deploy struct {
	Resources resources `yaml:"resources"`
}

type resources struct {
	Limits       limits  `yaml:"limits"`
	Reservations *limits `yaml:"reservations,omitempty"`
}

type limits struct {
	CPUs   string `yaml:"cpus,omitempty"`
	Memory string `yaml:"memory"`
}

func (f *composeFile) marshal() (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// builder accumulates one engine's Compose file and Plan.
type builder struct {
	engine   *Engine
	opts     Options
	image    string
	dataPath string
	file     composeFile
	plan     *Plan
}

// secret registers a generated credential and returns the ${VAR}
// interpolation that stands for it.
func (b *builder) secret(envVar, kind, name, username string) string {
	for _, s := range b.plan.Secrets {
		if s.EnvVar == envVar {
			return "${" + envVar + "}"
		}
	}
	b.plan.Secrets = append(b.plan.Secrets, SecretSpec{EnvVar: envVar, Kind: kind, Name: name, Username: username})
	return "${" + envVar + "}"
}

// adminPassword is the common case: the admin login's password.
func (b *builder) adminPassword() string {
	return b.secret("DB_PASSWORD", "admin", "Administrator password", b.opts.Username)
}

// hostBinding is the address ports are published on.
func (b *builder) hostBinding() string {
	if b.opts.Access == "remote" {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

func (b *builder) publish(hostPort, containerPort int) string {
	return fmt.Sprintf("%s:%d:%d", b.hostBinding(), hostPort, containerPort)
}

// memoryMB returns a fraction of the memory limit, at least floor.
func (b *builder) memoryMB(fraction float64, floor int) int {
	v := int(float64(b.opts.MemoryMB) * fraction)
	if v < floor {
		return floor
	}
	return v
}

func (b *builder) restartPolicy() string {
	if b.opts.Profile == "production" {
		return "always"
	}
	return "unless-stopped"
}

func (b *builder) resourceLimits(memoryMB int, cpus float64) *deploy {
	d := &deploy{Resources: resources{Limits: limits{
		CPUs:   strconv.FormatFloat(cpus, 'f', -1, 64),
		Memory: fmt.Sprintf("%dM", memoryMB),
	}}}
	if b.opts.Profile == "production" {
		// Reserve half the limit so the scheduler-less single host at
		// least documents the floor this database expects.
		d.Resources.Reservations = &limits{Memory: fmt.Sprintf("%dM", memoryMB/2)}
	}
	return d
}

// health builds a healthcheck; production checks more often and gives up
// sooner, so a failing database is reported unhealthy faster.
func (b *builder) health(startPeriod string, test ...string) *healthcheck {
	h := &healthcheck{Test: test, Interval: "15s", Timeout: "10s", Retries: 10, StartPeriod: startPeriod}
	if b.opts.Profile == "production" {
		h.Interval, h.Retries = "10s", 6
	}
	return h
}

// addService adds a service with the options' standard settings: restart
// policy, resource limits, labels. volumes maps volume name → mount path;
// each becomes a named volume.
func (b *builder) addService(name string, s *service, volumes map[string]string) *service {
	if s.Image == "" {
		s.Image = b.image
	}
	s.Restart = b.restartPolicy()
	if s.Deploy == nil {
		s.Deploy = b.resourceLimits(b.opts.MemoryMB, b.opts.CPUs)
	}
	s.Labels = map[string]string{
		"io.pspocketedge.database.engine":  b.engine.ID,
		"io.pspocketedge.database.profile": b.opts.Profile,
	}
	names := make([]string, 0, len(volumes))
	for v := range volumes {
		names = append(names, v)
	}
	sort.Strings(names)
	for _, v := range names {
		s.Volumes = append(s.Volumes, v+":"+volumes[v])
		if b.file.Volumes == nil {
			b.file.Volumes = map[string]*struct{}{}
		}
		b.file.Volumes[v] = nil
	}
	b.file.Services[name] = s
	return s
}

// primary adds the service clients connect to, publishing the engine's
// port(s).
func (b *builder) primary(name string, s *service, volumes map[string]string) *service {
	s.Ports = append([]string{b.publish(b.opts.Port, b.engine.Port.Container)}, s.Ports...)
	for _, p := range b.engine.ExtraHostPorts(b.opts.Port) {
		s.Ports = append(s.Ports, b.publish(p.Host, p.Container))
	}
	b.plan.Service = name
	return b.addService(name, s, volumes)
}
