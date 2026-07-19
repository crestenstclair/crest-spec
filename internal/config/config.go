package config

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/kelseyhightower/envconfig"
)

const DefaultDispatchTimeout = 2 * time.Minute

type Config struct {
	HTTPAddr string `envconfig:"HTTP_ADDR"`

	GenerateModel    string        `envconfig:"GENERATE_MODEL" default:"claude-sonnet-5"`
	MaxRetries       int           `envconfig:"MAX_RETRIES" default:"3"`
	WaveMaxRetries   int           `envconfig:"WAVE_MAX_RETRIES" default:"2"`
	SpecDir          string        `envconfig:"SPEC_DIR" default:"./spec"`
	TypeCheckCommand string        `envconfig:"TYPE_CHECK_CMD"`
	TestCommand      string        `envconfig:"TEST_CMD"`
	Mode             string        `envconfig:"MODE" default:"default"`
	Evolve           string        `envconfig:"EVOLVE" default:"all"`
	DispatchTimeout  time.Duration `envconfig:"DISPATCH_TIMEOUT" default:"2m"`
	ExecutionTimeout time.Duration `envconfig:"EXECUTION_TIMEOUT" default:"30m"`
}

// EffectiveDispatchTimeout preserves the production default for Config values
// built directly in tests or embeddings rather than through New. A zero-value
// dispatch timeout must not turn the recovery cutoff into "right now".
func (c *Config) EffectiveDispatchTimeout() time.Duration {
	if c == nil || c.DispatchTimeout == 0 {
		return DefaultDispatchTimeout
	}
	return c.DispatchTimeout
}

func New() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("CREST_SPEC", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func Help() {
	w := tabwriter.NewWriter(os.Stderr, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "crest-spec — declarative code generation MCP server")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Environment variables:")
	fmt.Fprintln(w)
	_ = envconfig.Usagef("CREST_SPEC", &Config{}, w, envconfig.DefaultTableFormat)
	w.Flush()
}
