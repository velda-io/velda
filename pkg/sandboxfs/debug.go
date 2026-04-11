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
package sandboxfs

import (
	"html/template"
	"net/http"
	"time"
)

var sandboxfsDebugTemplate = template.Must(template.New("sandboxfs-debug").Funcs(template.FuncMap{
	"since": func(ts time.Time) string {
		if ts.IsZero() {
			return ""
		}
		return time.Since(ts).Round(time.Millisecond).String()
	},
}).Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>sandboxfs debug</title>
  <style>
    body { font-family: "Iosevka", "SFMono-Regular", Consolas, monospace; margin: 24px; background: #f7f4ea; color: #1f1c17; }
    h1, h2 { margin-bottom: 8px; }
    a { color: #0a5c52; }
    table { width: 100%; border-collapse: collapse; margin-bottom: 24px; background: #fffdf6; }
    th, td { border: 1px solid #d9d0bc; padding: 8px 10px; text-align: left; vertical-align: top; }
    th { background: #efe4cb; }
    .empty { padding: 12px; background: #fffdf6; border: 1px solid #d9d0bc; margin-bottom: 24px; }
  </style>
</head>
<body>
  <h1>sandboxfs debug</h1>
  <p><a href="/debug/pprof/">pprof</a> | <a href="/metrics">metrics</a> | <a href="/debug/sandboxfs/state">json</a></p>
  <h2>Open file handles ({{len .OpenHandles}})</h2>
  {{if .OpenHandles}}
  <table>
    <tr><th>ID</th><th>Kind</th><th>Access</th><th>Backing</th><th>Cache ops</th><th>Open for</th><th>Path</th></tr>
    {{range .OpenHandles}}
    <tr>
      <td>{{.ID}}</td>
      <td>{{.Kind}}</td>
      <td>{{.Access}}</td>
      <td>{{.Backing}}</td>
      <td>{{.CacheOps}}</td>
      <td>{{since .OpenedAt}}</td>
      <td>{{.Path}}</td>
    </tr>
    {{end}}
  </table>
  {{else}}
  <div class="empty">No open handles.</div>
  {{end}}
  <h2>In-progress ops ({{len .InProgressOps}})</h2>
  {{if .InProgressOps}}
  <table>
    <tr><th>ID</th><th>Kind</th><th>Running for</th><th>Path</th><th>Detail</th></tr>
    {{range .InProgressOps}}
    <tr>
      <td>{{.ID}}</td>
      <td>{{.Kind}}</td>
      <td>{{since .StartedAt}}</td>
      <td>{{.Path}}</td>
      <td>{{.Detail}}</td>
    </tr>
    {{end}}
  </table>
  {{else}}
  <div class="empty">No in-progress operations.</div>
  {{end}}
</body>
</html>`))

func ServeSandboxfsDebugUI(w http.ResponseWriter, server *VeldaServer) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := sandboxfsDebugTemplate.Execute(w, server.DebugSnapshot()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
