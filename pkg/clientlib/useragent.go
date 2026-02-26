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
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	velda "velda.io/velda"
)

var (
	cachedUserAgent string
	userAgentOnce   sync.Once
)

// getClientUserAgent returns a user-agent string of the form:
//
//	velda/VERSION (GOOS; GOARCH; Kernel/KERNEL_RELEASE)
//
// The kernel release is obtained once by running "uname -r" and cached for the
// lifetime of the process. On platforms where uname is unavailable the kernel
// field is omitted.
func getClientUserAgent() string {
	userAgentOnce.Do(func() {
		kernel := ""
		if out, err := exec.Command("uname", "-r").Output(); err == nil {
			kernel = strings.TrimSpace(string(out))
		}
		if kernel != "" {
			cachedUserAgent = fmt.Sprintf("velda/%s (%s; %s; Kernel/%s)",
				velda.GetVersion(), runtime.GOOS, runtime.GOARCH, kernel)
		} else {
			cachedUserAgent = fmt.Sprintf("velda/%s (%s; %s)",
				velda.GetVersion(), runtime.GOOS, runtime.GOARCH)
		}
	})
	return cachedUserAgent
}
