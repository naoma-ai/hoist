package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type config struct {
	Project  string                   `yaml:"project"`
	Nodes    map[string]string        `yaml:"nodes"`
	Services map[string]serviceConfig `yaml:"services"`
	Hooks    hooksConfig              `yaml:"hooks"`
}

type hooksConfig struct {
	PostDeploy string `yaml:"post_deploy"`
}

type serviceConfig struct {
	Type        string               `yaml:"type"`
	Image       string               `yaml:"image"`
	Port        int                  `yaml:"port"`
	Healthcheck string               `yaml:"healthcheck"`
	Schedule    string               `yaml:"schedule"` // cron expression (cronjob only)
	Command     string               `yaml:"command"`  // container entrypoint override (cronjob only, optional)
	Env         map[string]envConfig `yaml:"env"`
}

type envConfig struct {
	// Server + cronjob fields
	Node    string `yaml:"node"`
	Host    string `yaml:"host"` // server only
	EnvFile string `yaml:"envfile"`
	// Static fields
	Bucket     string `yaml:"bucket"`
	CloudFront string `yaml:"cloudfront"`
}

func loadConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("reading config: %w", err)
	}

	var cfg config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("parsing config: %w", err)
	}

	if err := validateConfig(cfg); err != nil {
		return config{}, err
	}

	return cfg, nil
}

func validateConfig(cfg config) error {
	if cfg.Project == "" {
		return fmt.Errorf("missing project name")
	}

	if len(cfg.Services) == 0 {
		return fmt.Errorf("no services defined")
	}

	for name, svc := range cfg.Services {
		if svc.Type != "server" && svc.Type != "static" && svc.Type != "cronjob" {
			return fmt.Errorf("service %q: unknown type %q (must be \"server\", \"static\", or \"cronjob\")", name, svc.Type)
		}

		switch svc.Type {
		case "server":
			if svc.Image == "" {
				return fmt.Errorf("service %q: missing image", name)
			}
			if svc.Port == 0 {
				return fmt.Errorf("service %q: missing port", name)
			}
			if svc.Healthcheck == "" {
				return fmt.Errorf("service %q: missing healthcheck", name)
			}
		case "cronjob":
			if svc.Image == "" {
				return fmt.Errorf("service %q: missing image", name)
			}
			if svc.Schedule == "" {
				return fmt.Errorf("service %q: missing schedule", name)
			}
			if svc.Port != 0 {
				return fmt.Errorf("service %q: cronjob must not have port", name)
			}
			if svc.Healthcheck != "" {
				return fmt.Errorf("service %q: cronjob must not have healthcheck", name)
			}
		}

		if len(svc.Env) == 0 {
			return fmt.Errorf("service %q: no environments defined", name)
		}

		for envName, env := range svc.Env {
			switch svc.Type {
			case "server":
				if env.Node == "" {
					return fmt.Errorf("service %q env %q: missing node", name, envName)
				}
				if _, ok := cfg.Nodes[env.Node]; !ok {
					return fmt.Errorf("service %q env %q: node %q not defined in nodes", name, envName, env.Node)
				}
				if env.Host == "" {
					return fmt.Errorf("service %q env %q: missing host", name, envName)
				}
				if env.EnvFile == "" {
					return fmt.Errorf("service %q env %q: missing envfile", name, envName)
				}
			case "static":
				if env.Bucket == "" {
					return fmt.Errorf("service %q env %q: missing bucket", name, envName)
				}
				if env.CloudFront == "" {
					return fmt.Errorf("service %q env %q: missing cloudfront", name, envName)
				}
			case "cronjob":
				if env.Node == "" {
					return fmt.Errorf("service %q env %q: missing node", name, envName)
				}
				if _, ok := cfg.Nodes[env.Node]; !ok {
					return fmt.Errorf("service %q env %q: node %q not defined in nodes", name, envName, env.Node)
				}
				if env.EnvFile == "" {
					return fmt.Errorf("service %q env %q: missing envfile", name, envName)
				}
			}
		}
	}

	return nil
}
