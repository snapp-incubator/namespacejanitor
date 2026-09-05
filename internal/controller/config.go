package controller

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"go.yaml.in/yaml/v3"
)

type OperatorConfig struct {
	Region        string             `yaml:"region"`
	Lifecycle     LifecycleConfig    `yaml:"lifecycle"`
	Notifications NotificationConfig `yaml:"notifications"`
}

// LifecycleConfig holds all timing thresholds for the namespace lifecycle.
// Durations are specified as Go duration strings (e.g., "72h", "336h", "2s").
type LifecycleConfig struct {
	CreationNotification  bool     `yaml:"creationNotification"`
	YellowThreshold       Duration `yaml:"yellowThreshold"`
	RedThreshold          Duration `yaml:"redThreshold"`
	FinalWarningThreshold Duration `yaml:"finalWarningThreshold"`
	DeleteThreshold       Duration `yaml:"deleteThreshold"`
	ExcludeNamespaces     []string `yaml:"excludeNamespaces"`
}

// Duration wraps time.Duration for YAML unmarshaling.
// Accepts Go duration strings like "72h", "336h", "2s", "5m".
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (interface{}, error) {
	return d.String(), nil
}

// NotificationConfig holds notification channel settings.
type NotificationConfig struct {
	Mattermost MattermostConfig `yaml:"mattermost"`
	Kafka      KafkaConfig      `yaml:"kafka"`
}

// MattermostConfig holds Mattermost webhook settings.
type MattermostConfig struct {
	Webhook string `yaml:"webhook"`
}

// KafkaConfig holds Kafka connection settings.
type KafkaConfig struct {
	Broker string `yaml:"broker"`
	Topic  string `yaml:"topic"`
	Group  string `yaml:"group"`
}

// DefaultOperatorConfig returns the default production configuration.
func DefaultOperatorConfig() OperatorConfig {
	return OperatorConfig{
		Lifecycle: LifecycleConfig{
			CreationNotification:  true,
			YellowThreshold:       Duration{72 * time.Hour},  // 3 days
			RedThreshold:          Duration{336 * time.Hour}, // 14 days
			FinalWarningThreshold: Duration{720 * time.Hour}, // 30 days
			DeleteThreshold:       Duration{816 * time.Hour}, // 34 days
			ExcludeNamespaces: []string{
				"^openshift-.*",
				"^kube-.*",
				"^default$",
				"^kube-public$",
				"^kube-node-lease$",
				"^snappcloud-.*",
				".*-operator-system$",
				"^argocd$",
				"^monitoring$",
				"^cert-manager$",
			},
		},
		Notifications: NotificationConfig{
			Mattermost: MattermostConfig{},
			Kafka:      KafkaConfig{},
		},
	}
}

// LoadConfig reads and parses the operator configuration from a YAML file.
// If the file does not exist or is empty, it returns the default config.
func LoadConfig(path string) (*OperatorConfig, error) {
	cfg := DefaultOperatorConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &cfg, nil
		}
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	if len(data) == 0 {
		return &cfg, nil
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Validate thresholds are in the right order
	l := cfg.Lifecycle
	if l.YellowThreshold.Duration <= 0 {
		return nil, fmt.Errorf("lifecycle.yellowThreshold must be positive")
	}
	if l.RedThreshold.Duration <= l.YellowThreshold.Duration {
		return nil, fmt.Errorf("lifecycle.redThreshold must be greater than yellowThreshold")
	}
	if l.FinalWarningThreshold.Duration <= l.RedThreshold.Duration {
		return nil, fmt.Errorf("lifecycle.finalWarningThreshold must be greater than redThreshold")
	}
	if l.DeleteThreshold.Duration <= l.FinalWarningThreshold.Duration {
		return nil, fmt.Errorf("lifecycle.deleteThreshold must be greater than finalWarningThreshold")
	}

	return &cfg, nil
}

// NamespaceExcluder holds pre-compiled regex patterns for fast namespace exclusion checks.
type NamespaceExcluder struct {
	patterns []*regexp.Regexp
}

// NewNamespaceExcluder compiles the exclusion patterns from config.
func NewNamespaceExcluder(patterns []string) (*NamespaceExcluder, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		r, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid excludeNamespaces pattern %q: %w", p, err)
		}
		compiled = append(compiled, r)
	}
	return &NamespaceExcluder{patterns: compiled}, nil
}

// IsExcluded returns true if the namespace name matches any exclusion pattern.
func (e *NamespaceExcluder) IsExcluded(name string) bool {
	for _, r := range e.patterns {
		if r.MatchString(name) {
			return true
		}
	}
	return false
}
