// Package config loads the exporter YAML configuration.
//
// The layout mirrors snmp_exporter: named "auths" hold credentials, named
// "modules" hold what to collect and how hard to poll. A probe request picks
// one of each.
package config

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
	"gopkg.in/yaml.v3"

	"raisecom-snmp-exporter/internal/snmp"
)

// Collector names accepted in modules.
const (
	CollectorSystem     = "system"
	CollectorInterfaces = "interfaces"
	CollectorPoE        = "poe"
	CollectorSFP        = "sfp"
)

// AllCollectors is the default collector set, in execution order.
var AllCollectors = []string{CollectorSystem, CollectorInterfaces, CollectorPoE, CollectorSFP}

// Config is the root document.
type Config struct {
	Auths   map[string]*Auth   `yaml:"auths"`
	Modules map[string]*Module `yaml:"modules"`
}

// Auth holds SNMP credentials.
type Auth struct {
	Version   int    `yaml:"version"` // 1, 2 (v2c) or 3
	Community Secret `yaml:"community"`
	Port      uint16 `yaml:"port"`

	Username      string `yaml:"username"`
	SecurityLevel string `yaml:"security_level"` // noAuthNoPriv | authNoPriv | authPriv
	AuthProtocol  string `yaml:"auth_protocol"`  // MD5 | SHA | SHA224 | SHA256 | SHA384 | SHA512
	AuthPassword  Secret `yaml:"auth_password"`
	PrivProtocol  string `yaml:"priv_protocol"` // DES | AES | AES192 | AES256 | AES192C | AES256C
	PrivPassword  Secret `yaml:"priv_password"`
	ContextName   string `yaml:"context_name"`
}

// Module selects collectors and polling parameters.
type Module struct {
	Collectors     []string      `yaml:"collectors"`
	Timeout        time.Duration `yaml:"timeout"`
	Retries        *int          `yaml:"retries"`
	MaxRepetitions uint32        `yaml:"max_repetitions"`
	// PoEClass reads pethPsePortPowerClassifications for ports delivering power.
	// ROS answers it by querying the PoE controller (~80 ms per port), so it can be disabled.
	PoEClass *bool `yaml:"poe_class"`
}

// Has reports whether the module enables collector name.
func (m *Module) Has(name string) bool { return slices.Contains(m.Collectors, name) }

// WantPoEClass reports whether to read the PoE class column.
func (m *Module) WantPoEClass() bool { return m.PoEClass == nil || *m.PoEClass }

// Secret is a string that may reference an environment variable as ${NAME}
// and is never printed back.
type Secret string

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// UnmarshalYAML expands ${VAR} references. Only the braced form is expanded so
// passwords containing a bare '$' are left alone.
func (s *Secret) UnmarshalYAML(n *yaml.Node) error {
	var raw string
	if err := n.Decode(&raw); err != nil {
		return err
	}
	var missing []string
	out := envRef.ReplaceAllStringFunc(raw, func(m string) string {
		name := envRef.FindStringSubmatch(m)[1]
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
		}
		return v
	})
	if len(missing) > 0 {
		return fmt.Errorf("line %d: environment variable(s) not set: %s", n.Line, strings.Join(missing, ", "))
	}
	*s = Secret(out)
	return nil
}

// MarshalYAML hides the value.
func (s Secret) MarshalYAML() (any, error) {
	if s == "" {
		return "", nil
	}
	return "<secret>", nil
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse decodes and validates a config document.
func Parse(b []byte) (*Config, error) {
	c := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) validate() error {
	if len(c.Auths) == 0 {
		return fmt.Errorf("config: at least one auth is required")
	}
	if c.Modules == nil {
		c.Modules = map[string]*Module{}
	}
	if _, ok := c.Modules["default"]; !ok {
		c.Modules["default"] = &Module{}
	}
	for name, a := range c.Auths {
		if a == nil {
			return fmt.Errorf("auth %q: empty", name)
		}
		if _, err := a.Params(); err != nil {
			return fmt.Errorf("auth %q: %w", name, err)
		}
	}
	for name, m := range c.Modules {
		if m == nil {
			m = &Module{}
			c.Modules[name] = m
		}
		if len(m.Collectors) == 0 {
			m.Collectors = slices.Clone(AllCollectors)
		}
		for _, col := range m.Collectors {
			if !slices.Contains(AllCollectors, col) {
				return fmt.Errorf("module %q: unknown collector %q (known: %s)", name, col, strings.Join(AllCollectors, ", "))
			}
		}
		if m.Timeout == 0 {
			m.Timeout = 5 * time.Second
		}
		if m.Retries == nil {
			r := 1
			m.Retries = &r
		}
		if m.MaxRepetitions == 0 {
			m.MaxRepetitions = 25
		}
	}
	return nil
}

// Params converts the auth into connection parameters (target filled in by the caller).
func (a *Auth) Params() (snmp.Params, error) {
	p := snmp.Params{Port: a.Port, Community: string(a.Community)}
	switch a.Version {
	case 1:
		p.Version = gosnmp.Version1
	case 0, 2:
		p.Version = gosnmp.Version2c
	case 3:
		p.Version = gosnmp.Version3
	default:
		return p, fmt.Errorf("unsupported version %d", a.Version)
	}
	if p.Version != gosnmp.Version3 {
		if p.Community == "" {
			return p, fmt.Errorf("community is required for v%d", a.Version)
		}
		return p, nil
	}
	if a.Username == "" {
		return p, fmt.Errorf("username is required for v3")
	}
	p.Username = a.Username
	p.ContextName = a.ContextName
	switch strings.ToLower(a.SecurityLevel) {
	case "", "noauthnopriv":
		p.SecurityLevel = gosnmp.NoAuthNoPriv
	case "authnopriv":
		p.SecurityLevel = gosnmp.AuthNoPriv
	case "authpriv":
		p.SecurityLevel = gosnmp.AuthPriv
	default:
		return p, fmt.Errorf("unknown security_level %q", a.SecurityLevel)
	}
	if p.SecurityLevel != gosnmp.NoAuthNoPriv {
		proto, ok := authProtocols[strings.ToUpper(a.AuthProtocol)]
		if !ok {
			return p, fmt.Errorf("unknown auth_protocol %q", a.AuthProtocol)
		}
		if a.AuthPassword == "" {
			return p, fmt.Errorf("auth_password is required for %s", a.SecurityLevel)
		}
		p.AuthProtocol, p.AuthPassword = proto, string(a.AuthPassword)
	}
	if p.SecurityLevel == gosnmp.AuthPriv {
		proto, ok := privProtocols[strings.ToUpper(a.PrivProtocol)]
		if !ok {
			return p, fmt.Errorf("unknown priv_protocol %q", a.PrivProtocol)
		}
		if a.PrivPassword == "" {
			return p, fmt.Errorf("priv_password is required for authPriv")
		}
		p.PrivProtocol, p.PrivPassword = proto, string(a.PrivPassword)
	}
	return p, nil
}

var authProtocols = map[string]gosnmp.SnmpV3AuthProtocol{
	"":       gosnmp.SHA,
	"MD5":    gosnmp.MD5,
	"SHA":    gosnmp.SHA,
	"SHA224": gosnmp.SHA224,
	"SHA256": gosnmp.SHA256,
	"SHA384": gosnmp.SHA384,
	"SHA512": gosnmp.SHA512,
}

var privProtocols = map[string]gosnmp.SnmpV3PrivProtocol{
	"":        gosnmp.AES,
	"DES":     gosnmp.DES,
	"AES":     gosnmp.AES,
	"AES192":  gosnmp.AES192,
	"AES256":  gosnmp.AES256,
	"AES192C": gosnmp.AES192C,
	"AES256C": gosnmp.AES256C,
}
