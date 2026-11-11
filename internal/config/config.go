// Package config holds Local Lid's own settings and the tokens it minted.
//
// One JSON file, private to the user (0600). It holds only a
// Viewer token per project: writes mint a short-lived Operate token each time.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultListen = "127.0.0.1:47790"

type Config struct {
	DaemonURL     string    `json:"daemon_url"`
	Listen        string    `json:"listen"`
	PortalBaseURL string    `json:"portal_base_url"`
	ReplayEnabled bool      `json:"replay_enabled"`
	Projects      []Project `json:"projects"`
	// TokenNames maps a daemon token id to a human name, because the daemon's
	// last_actor is only "operate:<token-id>". Filled for tokens Local Lid
	// mints; other tools (datumctl-docker) can add theirs later.
	TokenNames map[string]string `json:"token_names"`
	// ControlSecret authorizes CLI commands (inspect, replay) against the
	// running server. Browsers never see it.
	ControlSecret string `json:"control_secret"`
	// SetupTokenFile is the Connect daemon setup token init used, so later commands
	// (audit --daemon) talk to the same daemon instead of guessing a path.
	SetupTokenFile string `json:"setup_token_file,omitempty"`
}

type Project struct {
	ID            string `json:"id"`
	ViewerTokenID string `json:"viewer_token_id"`
	ViewerToken   string `json:"viewer_token"`
	// Deprecated: pre-1d configs stored an Operate token here. serve
	// revokes it and clears these fields on start.
	OperateTokenID string `json:"operate_token_id,omitempty"`
	OperateToken   string `json:"operate_token,omitempty"`
}

func Default() *Config {
	return &Config{
		Listen:        DefaultListen,
		PortalBaseURL: "https://cloud.datum.net",
		ReplayEnabled: true,
		TokenNames:    map[string]string{},
	}
}

// Path is <user config dir>/local-lid/config.json, or LOCAL_LID_CONFIG.
func Path() (string, error) {
	if p := os.Getenv("LOCAL_LID_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "local-lid", "config.json"), nil
}

func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no config at %s: run `local-lid init` first", p)
	}
	if err != nil {
		return nil, err
	}
	c := Default()
	if err := json.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	if c.TokenNames == nil {
		c.TokenNames = map[string]string{}
	}
	return c, nil
}

// LoadOrDefault is for `init`, which may run before any config exists.
func LoadOrDefault() *Config {
	c, err := Load()
	if err != nil {
		return Default()
	}
	return c
}

func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (c *Config) Project(id string) *Project {
	for i := range c.Projects {
		if c.Projects[i].ID == id {
			return &c.Projects[i]
		}
	}
	return nil
}

func (c *Config) UpsertProject(p Project) {
	if existing := c.Project(p.ID); existing != nil {
		*existing = p
		return
	}
	c.Projects = append(c.Projects, p)
}

// EnsureControlSecret creates the CLI control secret on first use.
func (c *Config) EnsureControlSecret() (changed bool) {
	if c.ControlSecret != "" {
		return false
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	c.ControlSecret = hex.EncodeToString(b)
	return true
}

// DefaultProject is the only configured project, or "" if there are several.
func (c *Config) DefaultProject() string {
	if len(c.Projects) == 1 {
		return c.Projects[0].ID
	}
	return ""
}
