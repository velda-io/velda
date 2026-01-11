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
	"fmt"
	"log"
	"sync/atomic"

	"github.com/spf13/cobra"
	"velda.io/velda/pkg/sandboxfs"
)

const xattrCacheName = "user.veldafs.cache"

// cacheBackfillKeysCmd represents the cache backfill-cache-keys command
var cacheBackfillKeysCmd = &cobra.Command{
	Use:   "backfill-cache-keys <directory>",
	Short: "Backfill xattr cache keys for all files in a directory",
	Long: `Recursively walks through a directory and adds xattr cache keys for all files
where the key doesn't exist or is outdated. This computes the SHA256 hash and sets
the xattr, but does NOT download files to the cache. This is useful for pre-populating
cache metadata for a directory tree.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		directory := args[0]

		verbose, _ := cmd.Flags().GetBool("verbose")

		log.Printf("Backfilling cache keys for directory: %s", directory)

		var totalFiles, updatedFiles, skippedFiles, errorFiles int64

		// Progress callback
		progressCallback := func(path string, updated bool, err error) {
			atomic.AddInt64(&totalFiles, 1)
			if err != nil {
				atomic.AddInt64(&errorFiles, 1)
				if verbose {
					log.Printf("Error processing %s: %v", path, err)
				}
			} else if updated {
				atomic.AddInt64(&updatedFiles, 1)
				if verbose {
					log.Printf("Updated: %s", path)
				}
			} else {
				atomic.AddInt64(&skippedFiles, 1)
				if verbose {
					log.Printf("Skipped (already up-to-date): %s", path)
				}
			}

			// Print progress every 100 files
			if totalFiles%100 == 0 {
				log.Printf("Progress: %d files processed (%d updated, %d skipped, %d errors)",
					totalFiles, updatedFiles, skippedFiles, errorFiles)
			}
		}

		// Backfill cache keys
		err := sandboxfs.BackfillCacheKeys(directory, xattrCacheName, progressCallback)
		cobra.CheckErr(err)

		// Print summary
		fmt.Printf("\nSummary:\n")
		fmt.Printf("  Total files processed: %d\n", totalFiles)
		fmt.Printf("  Files updated: %d\n", updatedFiles)
		fmt.Printf("  Files skipped (already up-to-date): %d\n", skippedFiles)
		fmt.Printf("  Files with errors: %d\n", errorFiles)
	},
}

func init() {
	AgentCmd.AddCommand(cacheBackfillKeysCmd)
	cacheBackfillKeysCmd.Flags().BoolP("verbose", "v", false, "Print verbose progress information")
}
