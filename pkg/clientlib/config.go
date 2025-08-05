// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package clientlib

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"

	agentpb "velda.io/velda/pkg/proto/agent"
	"velda.io/velda/pkg/utils"
)

var (
	configDir        string
	profile          string
	profileInput     string
	systemConfigPath string
	// Only generated if the agent is running in a sandbox context.
	agentConfig *agentpb.AgentConfig

	profileNotFoundError = errors.New("Profile not found")
)

func InitConfigFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().StringVar(&configDir, "config_dir", "", "config directory. Defaults to ~/.config/velda")
	cmd.PersistentFlags().StringVar(&profileInput, "profile", "", "The user profile to use.")
	cmd.PersistentFlags().StringVar(&systemConfigPath, "system_config", "/run/velda/velda.yaml", "Path to the system configuration file. Defaults to /run/velda/velda.yaml")
	cmd.PersistentFlags().MarkHidden("system_config")

	// Legacy flags.
	cmd.PersistentFlags().StringVar(&brokerAddrFlag, "broker", "novahub.dev:50051", "broker address")
	cmd.PersistentFlags().MarkHidden("broker")
}

func InitConfig() {
	_, err := os.Stat(systemConfigPath)
	if err != nil {
		// User login.
		if configDir == "" {
			// Try to use the user's home directory
			homedir, err := os.UserHomeDir()
			if err == nil {
				configDir = homedir + "/.config/velda"
				os.MkdirAll(configDir, 0755)
			}
		}
		profile = profileInput
		if profile == "" {
			profile, err = GlobalConfig().GetConfig("profile")
			if err != nil {
				log.Printf("Failed to get current profile: %v", err)
			}
		}
	} else {
		// Agent daemon.
		agentConfig = &agentpb.AgentConfig{}
		if err := utils.LoadProto(systemConfigPath, agentConfig); err != nil {
			log.Printf("Failed to load agent config: %v", err)
		}
		brokerAddrFlag = agentConfig.Broker.Address
	}
}

func GetConfigDir() string {
	return configDir
}

func IsInSession() bool {
	return agentConfig != nil && agentConfig.Session != ""
}

func GenerateAgentConfig(instance int64, session string) map[string]interface{} {
	return map[string]interface{}{
		"broker": map[string]interface{}{
			"address": agentConfig.Broker.Address,
		},
		"session":  session,
		"instance": instance,
	}
}

type Configs struct {
	Profile string
	db      *sql.DB
}

func newConfigs(profile string) (*Configs, error) {
	if configDir == "" {
		return nil, errors.New("config directory not set")
	}
	db, err := sql.Open("sqlite", configDir+"/config.db")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS config(profile TEXT, key TEXT, value TEXT, PRIMARY KEY(profile, key))")
	if err != nil {
		return nil, err
	}
	return &Configs{
		Profile: profile,
		db:      db,
	}, nil
}

func LoadConfigs(profile string) (*Configs, error) {
	cfg, err := newConfigs(profile)
	if err != nil {
		return nil, err
	}
	if e, _ := cfg.GetConfig("email"); e == "" {
		return cfg, fmt.Errorf("%w: %v", profileNotFoundError, profile)
	}
	return cfg, nil
}

func (c *Configs) SetConfig(key string, value string) error {
	_, err := c.db.Exec("INSERT INTO config(profile, key, value) VALUES($1, $2, $3) ON CONFLICT(profile, key) DO UPDATE SET value = $3", c.Profile, key, value)
	return err
}

func (c *Configs) GetConfig(key string) (string, error) {
	var value string
	err := c.db.QueryRow("SELECT value FROM config WHERE profile = $1 AND key = $2", c.Profile, key).Scan(&value)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	return value, nil
}

func (c *Configs) ListConfigs() (map[string]string, error) {
	rows, err := c.db.Query("SELECT key, value FROM config WHERE profile = $1", c.Profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configs := make(map[string]string)
	for rows.Next() {
		var key, value string
		err = rows.Scan(&key, &value)
		if err != nil {
			return nil, err
		}
		configs[key] = value
	}
	return configs, nil
}

func (c *Configs) DeleteConfig(key string) error {
	_, err := c.db.Exec("DELETE FROM config WHERE profile = $1 AND key = $2", c.Profile, key)
	return err
}

func (c *Configs) Close() {
	c.db.Close()
}

var currentConfig *Configs
var currentConfigErr error
var configInit sync.Once

func CurrentConfig() (*Configs, error) {
	configInit.Do(func() {
		var err error
		currentConfig, err = LoadConfigs(profile)
		if err != nil {
			currentConfigErr = err
			return
		}
	})
	return currentConfig, currentConfigErr
}

func InitCurrentConfig(brokerAddr string, newProfile bool) (bool, *Configs, error) {
	var created = false
	configInit.Do(func() {
		if newProfile {
			profile = profileInput
		}
		if profile == "" {
			profile = "temp"
			created = true
		}
		configs, err := LoadConfigs(profile)
		if errors.Is(err, profileNotFoundError) {
			created = true
			// Expected, will create a new config.
			err = configs.SetConfig("broker", brokerAddr)
		}
		if err != nil {
			currentConfigErr = err
			return
		}
		currentConfig = configs
	})
	return created, currentConfig, currentConfigErr
}

func MustCurrentConfig() *Configs {
	cfg, err := CurrentConfig()
	if err != nil {
		panic(err)
	}
	return cfg
}

var globalConfig *Configs
var globalConfigInit sync.Once

func GlobalConfig() *Configs {
	globalConfigInit.Do(func() {
		var err error
		globalConfig, err = newConfigs("global")
		if err != nil {
			panic(err)
		}
	})
	return globalConfig
}

func (cfg *Configs) RenameConfig(newProfile string) error {
	if newProfile == "temp" || newProfile == "global" {
		return errors.New("Invalid profile name")
	}
	oldProfile := cfg.Profile
	// Check if the profile already exists.
	var count int
	err := cfg.db.QueryRow("SELECT COUNT(*) FROM config WHERE profile = $1", newProfile).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return errors.New("Profile already exists")
	}

	_, err = cfg.db.Exec("UPDATE config SET profile = $1 WHERE profile = $2", newProfile, oldProfile)
	if err != nil {
		return err
	}
	err = RenameProfile(oldProfile, newProfile)
	if err != nil {
		return fmt.Errorf("Error updating auth provider profile: %w", err)
	}
	cfg.Profile = newProfile

	defaultProfile, err := GlobalConfig().GetConfig("profile")
	if err == nil && defaultProfile == oldProfile {
		err = GlobalConfig().SetConfig("profile", newProfile)
		if err != nil {
			return fmt.Errorf("Error setting default profile: %w", err)
		}
	}
	return err
}

func (cfg *Configs) MakeCurrent() error {
	err := GlobalConfig().SetConfig("profile", cfg.Profile)
	if err != nil {
		return fmt.Errorf("Error setting default profile: %w", err)
	}
	return nil
}

func ListProfiles() ([]string, error) {
	rows, err := GlobalConfig().db.Query("SELECT profile FROM config WHERE key='email'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := make([]string, 0)
	for rows.Next() {
		var profile string
		err = rows.Scan(&profile)
		if err != nil {
			return nil, err
		}
		if profile != "temp" && profile != "global" {
			profiles = append(profiles, profile)
		}
	}
	return profiles, nil
}

func DeleteCurrentProfile() error {
	cfg, err := CurrentConfig()
	if err != nil {
		return fmt.Errorf("Error getting current profile: %w", err)
	}
	if cfg.Profile == "temp" || cfg.Profile == "global" {
		return errors.New("Cannot delete temporary or global profile")
	}
	// Delete the profile from the config database.
	_, err = cfg.db.Exec("DELETE FROM config WHERE profile = $1", cfg.Profile)
	if err != nil {
		return fmt.Errorf("Error deleting profile: %w", err)
	}
	DeleteProfile(cfg.Profile)
	return nil
}

func GetAgentSandboxConfig() *agentpb.SandboxConfig {
	if agentConfig == nil {
		return &agentpb.SandboxConfig{}
	}
	return agentConfig.SandboxConfig
}

func GetAgentDaemonConfig() *agentpb.DaemonConfig {
	return agentConfig.DaemonConfig
}
