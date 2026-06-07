package config

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	APIKey         string        `envconfig:"API_KEY"`
	AgentPath      string        `envconfig:"AGENT_PATH" default:"claude"`
	DefaultModel   string        `envconfig:"DEFAULT_MODEL" default:"claude-sonnet-4-6"`
	Timeout        time.Duration `envconfig:"TIMEOUT" default:"0s"`
	MaxConcurrency int           `envconfig:"MAX_CONCURRENCY" default:"5"`
	HTTPAddr       string        `envconfig:"HTTP_ADDR"`

	GenerateModel    string `envconfig:"GENERATE_MODEL" default:"claude-sonnet-4-6"`
	VerifyModel      string `envconfig:"VERIFY_MODEL" default:"claude-sonnet-4-6"`
	MaxRetries       int    `envconfig:"MAX_RETRIES" default:"3"`
	WaveMaxRetries   int    `envconfig:"WAVE_MAX_RETRIES" default:"2"`
	SpecDir          string `envconfig:"SPEC_DIR" default:"./spec"`
	TypeCheckCommand string `envconfig:"TYPE_CHECK_CMD"`
	TestCommand      string `envconfig:"TEST_CMD"`
	Mode             string `envconfig:"MODE" default:"default"`
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
