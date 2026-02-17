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
package cmd

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

// loginCmd represents the login command
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to Velda.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		brokerAddr, _ := cmd.Flags().GetString("broker")
		profileProvided, _ := cmd.Flags().GetString("profile")
		newProfile, _ := cmd.Flags().GetBool("new-profile")
		newProfile = newProfile && profileProvided == ""
		created, cfg, err := clientlib.InitCurrentConfig(brokerAddr, newProfile)
		if err != nil {
			return fmt.Errorf("Error initializing config: %w", err)
		}
		// Generate a random state as a hex encoding of 64 bytes using a secure random generator
		stateBytes := make([]byte, 64)
		_, err = rand.Read(stateBytes)
		if err != nil {
			return fmt.Errorf("Error generating random state: %w", err)
		}
		state := fmt.Sprintf("%x", stateBytes)

		broker, err := cfg.GetConfig("broker")
		if err != nil {
			return fmt.Errorf("Error getting broker address: %w", err)
		}
		if broker == "" {
			return fmt.Errorf("Broker address not set. Please set the broker address using --broker flag or in the config file.")
		}
		loginUrl := fmt.Sprintf("%s/device?state=%s", broker, state)
		if !strings.HasPrefix(loginUrl, "http") {
			loginUrl = "http://" + loginUrl
		}
		apiMode, _ := cmd.Flags().GetString("api_mode")
		reader := bufio.NewReader(os.Stdin)
		if apiMode == "" {
			err = openBrowser(loginUrl)
			if err != nil {
				cmd.Printf("Failed to open browser automatically. Please open the URL manually.\n")
			}
			cmd.Printf("Please login at %s and provide the code.\n", loginUrl)
			cmd.Printf("Enter the code from the browser: ")
		} else if apiMode == "v1" {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", loginUrl)
		}
		code, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("Error reading code: %w", err)
		}
		code = strings.TrimSpace(code)
		if code == "" {
			return fmt.Errorf("Code cannot be empty")
		}
		authenticator, err := clientlib.GetAuthenticator()
		if err != nil {
			return fmt.Errorf("Error getting authenticator: %w", err)
		}
		resp, err := authenticator.GetAuthClient().AccessCodeLogin(cmd.Context(), &proto.AccessCodeLoginRequest{
			AccessCode: code,
			State:      state,
		})
		if err != nil {
			return fmt.Errorf("Error logging in: %w", err)
		}
		email := resp.Email
		err = authenticator.Login(cmd.Context(), resp)
		if err != nil {
			return err
		}
		cfg.SetConfig("email", email)
		if created {
			profileName := cfg.Profile
			if profileName == "" || profileName == "temp" {
				if apiMode == "v1" {
					fmt.Fprintf(cmd.OutOrStdout(), "%s", email)
				} else {
					cmd.Printf("Choose a profile name: [%s]:", email)
				}
				profileName, err = reader.ReadString('\n')
				if err != nil {
					return fmt.Errorf("Error reading profile name: %w", err)
				}
				profileName = strings.TrimSpace(profileName)
				if profileName == "" {
					profileName = email
				}
			}
			err = cfg.RenameConfig(profileName)
			if err != nil {
				return fmt.Errorf("Error renaming profile: %w", err)
			}
		}
		if apiMode == "" {
			cmd.PrintErrf("Login saved to profile %s\n", cfg.Profile)
		}
		return cfg.MakeCurrent()
	},
}

func init() {
	authCmd.AddCommand(loginCmd)

	loginCmd.Flags().String("broker", "novahub.dev:50051",
		("Broker address. If profile already exists," +
			" this will be ignored and use the existing broker address in config."))
	loginCmd.Flags().Bool("new-profile", false,
		"Create a new profile if --profile is not provided and no default profile exists.")
	loginCmd.Flags().String("api_mode", "", "Used for programatically access")
	loginCmd.Flags().MarkHidden("api_mode")
}
func openBrowser(url string) error {
	var cmd string
	var args []string

	switch goos := runtime.GOOS; goos {
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default:
		return fmt.Errorf("unsupported platform")
	}

	return exec.Command(cmd, args...).Start()
}
