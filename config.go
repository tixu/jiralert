package jiralert

import (
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

// ReadConfiguration parses the YAML input into a Config
func ReadConfiguration(configDir string) (*Config, error) {
	log.Info("loading configuration")
	cfg := &Config{}
	viper.AddConfigPath(configDir)
	viper.SetConfigName("jiralert")
	err := viper.ReadInConfig()

	if err != nil {
		return nil, err
	}

	err = viper.Unmarshal(cfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

//APIConfig contains API access fields (URL, user and password)
type APIConfig struct {
	// API access fields
	URL      string
	User     string
	Password string
}

// ReceiverConfig is the configuration for one receiver. It has a unique name and includes and issue fields (required -- e.g. project, issue type -- and optional -- e.g. priority).
type ReceiverConfig struct {
	Name string

	// Required issue fields
	Project     string
	IssueType   string
	Summary     string
	ReopenState string

	// Optional issue fields
	Priority          string
	Description       string
	Comment           string
	WontFixResolution string
	Fields            map[string]interface{}
	Components        []string

	// Label copy settings
	AddGroupLabels bool

	// Catches all undefined fields and must be empty after parsing.
	XXX map[string]interface{}
}

// Config is the top-level configuration for JIRAlert's config file.
type Config struct {
	
	Receivers []*ReceiverConfig
	Template  string

	// Catches all undefined fields and must be empty after parsing.
	XXX map[string]interface{} `yaml:",inline" json:"-"`
}

func (c Config) String() string {
	b, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Sprintf("<error creating config string: %s>", err)
	}
	return string(b)
}

// ReceiverByName loops the receiver list and returns the first instance with that name
func (c *Config) ReceiverByName(name string) *ReceiverConfig {
	for _, rc := range c.Receivers {
		if rc.Name == name {
			return rc
		}
	}
	return nil
}

func checkOverflow(m map[string]interface{}, ctx string) error {
	if len(m) > 0 {
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		return fmt.Errorf("unknown fields in %s: %s", ctx, strings.Join(keys, ", "))
	}
	return nil
}
