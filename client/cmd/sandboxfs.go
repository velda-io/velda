// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//go:build linux

package cmd

import (
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/sandboxfs"
)

// sandboxfsCmd represents the sandboxfs command
var sandboxfsCmd = &cobra.Command{
	Use:   "sandboxfs base target",
	Short: "Mount sandbox file system",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		if os.Getenv("VELDA_SANDBOX_PPROF") != "" {
			go func() {
				port := ":6063"
				http.Handle("/metrics", promhttp.Handler())
				cmd.Printf("PProf finished with %s", http.ListenAndServe(port, nil))
			}()
		}
		base := args[0]
		target := args[1]
		cacheDir, _ := cmd.Flags().GetString("cache-dir")
		snapshotMode, _ := cmd.Flags().GetBool("snapshot")

		var mountOpts []sandboxfs.MountOptions
		mountOpts = append(mountOpts, func(opt *sandboxfs.VeldaMountOptions) {
			opt.FuseOptions.FsName, _ = cmd.Flags().GetString("name")
			opt.FuseOptions.Debug = clientlib.Debug
		})

		if snapshotMode {
			mountOpts = append(mountOpts, sandboxfs.WithSnapshotMode())
		}

		noCacheMode, _ := cmd.Flags().GetBool("nocache")
		if noCacheMode {
			mountOpts = append(mountOpts, sandboxfs.WithNoCacheMode())
		}

		server, err := sandboxfs.MountWorkDir(base, target, cacheDir, mountOpts...)
		cobra.CheckErr(err)
		readyfd, _ := cmd.Flags().GetInt("readyfd")
		if readyfd != 0 {
			file := os.NewFile(uintptr(readyfd), "")
			_, err := file.WriteString("1")
			if err != nil {
				cobra.CheckErr(err)
			}
			err = file.Close()
			if err != nil {
				log.Printf("Failed to close readyfd: %v", err)
			}
		}
		server.Wait()
		log.Println("sandboxfs terminated")
	},
}

func init() {
	AgentCmd.AddCommand(sandboxfsCmd)
	sandboxfsCmd.Flags().Int("readyfd", 0, "File descriptor to signal when the mount is ready")
	sandboxfsCmd.Flags().String("name", "", "Name of the mount")
	sandboxfsCmd.Flags().String("cache-dir", "/tmp/velda_cas_cache", "Directory for caching")
	sandboxfsCmd.Flags().Bool("snapshot", false, "Enable snapshot mode for maximum IO performance (aggressive caching)")
	sandboxfsCmd.Flags().Bool("nocache", false, "Enable no-cache mode to disable caching")
}
