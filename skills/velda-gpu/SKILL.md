---
name: velda-gpu
description: Create and terminate GPU workloads on Velda from the CLI. Use for Velda Cloud GPU launches, H100 smoke tests, session cleanup, and deletion of temporary Velda instances.
---

# Velda GPU operations

A Velda instance is a persistent workspace. A session attaches compute from a pool to that workspace. Select a GPU pool when running the workload. The default `shell` pool uses CPUs. Use `velda run` from the host and `vrun` from inside a Velda session. [Velda workload guide](https://docs.velda.io/user-guide/running-workloads/)

Use the requested account and scope. Carry out billable launches and destructive cleanup when authorized by the task.

## Check access and choose compute

1. Use the installed CLI, including `~/.local/bin/velda` if it is outside `PATH`. Download the matching platform binary from [Velda releases](https://github.com/velda-io/velda/releases) if needed. Run `velda auth check` on Velda Cloud. If sign-in is required, start `velda auth login` in an interactive terminal and have the user enter the browser code there. The saved profile works for subsequent launches.
2. Run `velda instance list -a` and `velda pool list --region "$region"`. If the user authorizes an existing suitable instance, reuse its workspace. Otherwise create a uniquely named temporary instance. Record its name before the create request and its numeric ID once confirmed.
3. Check the region's live pool and rate. Match the GPU model, count, and VRAM to the workload. Estimate cost from the hourly rate and expected compute time. For an interruption-tolerant H100 check in `nebius-eu`, look for `h100-1s` (one H100 80 GB, spot) and `h100-1` (on demand). Recheck the pool and price each time.

The CLI pool list shows names and descriptions. On Velda Cloud, the regional REST catalog also gives `priceMicrosPerHour` and the image IDs usable with `instance create -i`. Use the saved CLI access token inside the HTTP client. A Cloud API key accepted by the central list endpoint may still return HTTP 401 on a regional operation. [Pool API](https://docs.velda.io/reference/api/pool-manager/) · [Instance API](https://docs.velda.io/reference/api/instance/)

```bash
python3 - "$region" <<'PY'
import http.client
import json
import shutil
import subprocess
import sys
from pathlib import Path
from urllib.parse import urlencode

cli = shutil.which("velda") or str(Path.home() / ".local/bin/velda")
token = subprocess.check_output([cli, "auth", "print-access-token"], text=True, timeout=10).strip()
connection = http.client.HTTPSConnection("velda.cloud", timeout=18)
query = urlencode({"region": sys.argv[1]})

for path, field in (("/rest/pool", "pools"), ("/rest/images", "images")):
    connection.request("GET", f"{path}?{query}", headers={"Authorization": f"Bearer {token}"})
    response = connection.getresponse()
    body = response.read()
    if response.status != 200:
        raise SystemExit(f"{path} returned HTTP {response.status}")
    print(json.dumps(json.loads(body)[field], indent=2))

connection.close()
PY
```

## Create the workspace and run the workload

For a new workspace, use a known image supported in the target region. For example, Ubuntu 24.04 in `nebius-eu` was listed as `3138f4d6-3962-4bf4-a2ab-3702b04fa6da` on 2026-11-19. Confirm the image is still listed before using that ID. If a public Docker image is a better fit, use `-d` in place of `-i` and wait for its remote initialization task to succeed. Save the initialization task ID so you can check or cancel it if the client disconnects. [Instance creation source](https://github.com/velda-io/velda/blob/244da37abe8d990d46d18e7dcb27aebf326889a0/client/cmd/instance_create.go)

```bash
velda instance create "$instance_name" -i "$image_id" --region "$region"
```

Run the GPU command directly from the host. Set a unique `service` name so you can identify this launch even after the CLI disconnects. Put all Velda flags before the remote command. Set a local allocation deadline and a remote runtime limit appropriate for the request. This example limits `nvidia-smi` to 15 seconds after the command starts.

```bash
velda run --instance "$instance_id" -P "$pool" --new-session -s "$service" \
  -I --tty no timeout 15s nvidia-smi
```

Use the connected launch for short probes. With the default connection-finish settings, Velda terminates the session when the last connection closes. For a requested detached workload, record the batch job ID, monitor `velda task get` and `velda task log`, and cancel the job if it exceeds the deadline. When the CLI supports `--email=false`, use it for batch jobs unless the user authorized an email. For an HTTP batch submission, set `workload.emailStatus` to `EMAIL_STATUS_DISABLED`. [Run source](https://github.com/velda-io/velda/blob/244da37abe8d990d46d18e7dcb27aebf326889a0/client/cmd/run.go) · [Task API](https://docs.velda.io/reference/api/task/)

## Terminate and verify

Arrange cleanup before launch. Run it on success, failure, or timeout.

1. List remaining sessions by this run's service name using `velda list-session -i "$instance_id" -s "$service" -o json`. If a session remains, use `velda kill-session -i "$instance_id" --session-id "$session_id"` and poll until the session is gone. If the kill returns an error after the workload exits, list again before retrying. Automatic teardown can finish during the kill request. If a queued or detached job remains, use `velda task cancel "$job_id"` and confirm its terminal state. If a session still fails to stop, use `velda kill-session -f` with the same instance and session IDs. [Broker API](https://docs.velda.io/reference/api/broker/)
2. For a temporary instance created for this run, save any requested outputs, end its remaining sessions or jobs, then run `velda instance delete "$instance_id"`. For a user's existing instance, delete the workspace only if the user requested deletion. Bulk cleanup requires identifying the intended account and checking ownership from the instance API before deletion. Deletion removes the workspace and its files. [Deletion source](https://github.com/velda-io/velda/blob/244da37abe8d990d46d18e7dcb27aebf326889a0/pkg/instances/instances.go)
3. Verify deleted IDs are absent from `velda instance list -a` or return HTTP 404 or `INSTANCE_STATUS_TERMINATED` from `GET /rest/instances/{id}`. Verify a retained instance separately with `list-session`. An authentication error or timeout leaves deletion unverified. Check Cloud billing when accessible and label a runtime-based cost as an estimate if the charge is pending.

## Recover from a timeout

- An HTTP 504 or local timeout can occur after Velda accepted the request. Look up the exact workspace name and inspect its sessions and any known task ID before retrying. Poll briefly for delayed creation. For a timed-out GPU launch, reconcile by the unique service name. Retry only after accounting for the first attempt. The REST `POST /rest/task/run` endpoint can create a workspace before scheduling the workload, so reconcile both resources if using it. [Task submission source](https://github.com/velda-io/velda/blob/244da37abe8d990d46d18e7dcb27aebf326889a0/pkg/tasks/task_service.go)
- If GPU allocation stalls, check the pool's `autoscalerStatus` in the live catalog. For repeated image listing and creation timeouts, report the region and observed responses. A different CLI or website entry point can still depend on the same regional service.
- For a timed probe, record UTC timestamps and monotonic durations for instance creation, GPU request to allocation, GPU request to first `nvidia-smi` output, and confirmed cleanup. Report the actual GPU model, VRAM, pool rate, measured runtime, cleanup status, and recorded or estimated cost. State explicitly which phases could be measured if the run failed.
