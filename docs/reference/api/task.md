---
title: Task
---

# Task

### Get Task

`GET /rest/task/info/{task_id}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `task_id` | string |  |

**Response** (200 OK)

`application/json` — [Task](#task)

**Example**

```http
GET /rest/task/info/{task_id} HTTP/1.1
Authorization: Bearer <token>
```

---

### List Tasks

`GET /rest/task/tasks/{parent_id}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `parent_id` | string |  |

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `page_size` | int32 |  |
| `page_token` | string |  |

**Response** (200 OK)

`application/json` — [TaskPageResult](#taskpageresult)

**Example**

```http
GET /rest/task/tasks/{parent_id} HTTP/1.1
Authorization: Bearer <token>
```

---

### Search Tasks

`GET /rest/task/search`

**Authorization**: Bearer `<token>`

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `label_filters` | string[] |  |
| `page_size` | int32 |  |
| `page_token` | string |  |

**Response** (200 OK)

`application/json` — [TaskPageResult](#taskpageresult)

**Example**

```http
GET /rest/task/search HTTP/1.1
Authorization: Bearer <token>
```

---

### Run Task

`POST /rest/task/run`

**Authorization**: Bearer `<token>`

**Request Body** (application/json)

| Field  &nbsp; &nbsp; &nbsp; &nbsp; &nbsp; | Type | Description |
|-------|------|-------------|
| `instance_id` | int64 | _(oneof target)_ |
| `instance_name` | string | _(oneof target)_ |
| `new_instance` | [NewInstance](#newinstance) | _(oneof target)_ |
| `pool` | string |  |
| `priority` | int64 |  |
| `labels` | string[] |  |
| `workload` | [Workload](#workload) |  |
| `snapshot_name` | string |  |

**Response** (200 OK)

`application/json` — [RunTaskResponse](#runtaskresponse)

**Example**

```http
POST /rest/task/run HTTP/1.1
Authorization: Bearer <token>
Content-Type: application/json
```

---

### Cancel Job

`POST /rest/task/cancel/{job_id}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `job_id` | string |  |

**Response** (200 OK)

Empty response.

**Example**

```http
POST /rest/task/cancel/{job_id} HTTP/1.1
Authorization: Bearer <token>
```

---

### Watch Task

`GET /rest/task/watch/{task_id}` _(server-streaming)_

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `task_id` | string |  |

**Response** (200 OK)

`application/json` — [Task](#task)

**Example**

```http
GET /rest/task/watch/{task_id} HTTP/1.1
Authorization: Bearer <token>
```

---

## Message Types

### BatchTaskResult

| Field | Type | Description |
|-------|------|-------------|
| `exit_code` | int32 | _(oneof _exit_code)_ |
| `terminated_signal` | int32 |  |

### CreateInstanceRequest

| Field | Type | Description |
|-------|------|-------------|
| `instance` | [Instance](#instance) |  |
| `image_name` | string | _(oneof source)_ |
| `snapshot` | [SnapshotReference](#snapshotreference) | _(oneof source)_ |
| `region` | string | Optional: specify region by name  If not specified, uses the current region.  Only used for hosted / enterprise. |
| `docker_image` | string | Optional: if set, the server will enqueue a remote initialization task  that imports this container image into the newly created instance. |
| `skip_docker_init_script` | bool | Optional: skip the post-install initialization script when  docker_image is set. |
| `docker_auth_username` | string | Optional: registry username for private docker_image pulls.  Used only when docker_image is set. |
| `docker_auth_password` | string | Optional: registry password for private docker_image pulls.  Used only when docker_image is set. |

### Instance

| Field | Type | Description |
|-------|------|-------------|
| `id` | int64 | Only to be filled by the server. |
| `instance_name` | string |  |
| `status` | [InstanceStatus](#instancestatus) |  |
| `init_task_id` | string | Optional: set by CreateInstance when docker_image initialization  is enqueued. |
| `owner_id` | int64 | Only to be filled by the server. |
| `org_id` | int64 | org_id is the organization this instance belongs to. |
| `region_display_info` | [RegionDisplayInfo](#regiondisplayinfo) | Region metadata for UI display. |

### RegionDisplayInfo

| Field | Type | Description |
|-------|------|-------------|
| `name` | string |  |
| `logo` | [RegionLogo](#regionlogo) |  |

### NewInstance

| Field | Type | Description |
|-------|------|-------------|
| `create_instance` | [CreateInstanceRequest](#createinstancerequest) |  |

### RunTaskResponse

| Field | Type | Description |
|-------|------|-------------|
| `task_id` | string |  |
| `instance_id` | int64 |  |

### SnapshotReference

| Field | Type | Description |
|-------|------|-------------|
| `instance_id` | int64 |  |
| `snapshot_name` | string |  |

### Task

| Field | Type | Description |
|-------|------|-------------|
| `id` | string |  |
| `status` | [TaskStatus](#taskstatus) |  |
| `instance_id` | int64 |  |
| `workload` | [Workload](#workload) |  |
| `pool` | string |  |
| `priority` | int64 |  |
| `labels` | string[] |  |
| `created_at` | string (RFC 3339) |  |
| `started_at` | string (RFC 3339) |  |
| `finished_at` | string (RFC 3339) |  |
| `resolved_at` | string (RFC 3339) | For task with sub-tasks, when all the sub-tasks are completed. |
| `batch_task_result` | [BatchTaskResult](#batchtaskresult) |  |
| `children_count` | int32 |  |
| `completed_children_count` | int32 |  |

### TaskPageResult

| Field | Type | Description |
|-------|------|-------------|
| `tasks` | [Task](#task)[] |  |
| `next_page_token` | string |  |

### Workload

| Field | Type | Description |
|-------|------|-------------|
| `command` | string | The binary path to execute. |
| `command_path` | string | If provided, the absolute path to the binary. |
| `args` | string[] | The arguments to pass to the binary. |
| `working_dir` | string | The working directory to execute the command in.  If empty, home directory will be used. |
| `environs` | string[] | The environment variables to set for the command.  Environment variables are passed in the form of key=value. |
| `shell` | bool | Whether to execute the command in a shell.  If true, command will be the shell script and args are ignored.  If command is empty, it will start the default shell at current directory. |
| `uid` | uint32 | The system user credentials to execute the command. |
| `gid` | uint32 |  |
| `groups` | uint32[] |  |
| `total_shards` | int32 | Total number of shards. |
| `shard_scheduling` | [ShardScheduling](#shardscheduling) |  |
| `shard_index` | int32 | 0-based index of the current shard.  -1 is used for parent group. |
| `login_user` | string | If set, will ignore uid/gid/groups and initialize the environment with  the default environment variables of the user. |
| `writable_dirs` | string[] | For batch jobs with snapshots: writable directories where changes persist |
| `snapshot_name` | string | For batch jobs with snapshots: the snapshot name to use as base |
| `stdin` | bytes | Optional non-streaming stdin payload for batch workloads. |

### InstanceStatus

| Value | Number |
|-------|--------|
| `INSTANCE_STATUS_UNSPECIFIED` | 0 |
| `INSTANCE_STATUS_ACTIVE` | 1 |
| `INSTANCE_STATUS_TERMINATED` | 2 |

### RegionLogo

| Value | Number |
|-------|--------|
| `REGION_LOGO_UNSPECIFIED` | 0 |
| `REGION_LOGO_GENERIC` | 1 |
| `REGION_LOGO_AWS` | 2 |
| `REGION_LOGO_GCP` | 3 |
| `REGION_LOGO_AZURE` | 4 |
| `REGION_LOGO_NEBIUS` | 5 |

### TaskStatus

| Value | Number |
|-------|--------|
| `TASK_STATUS_UNSPECIFIED` | 0 |
| `TASK_STATUS_PENDING` | 1 |
| `TASK_STATUS_QUEUEING` | 2 |
| `TASK_STATUS_RUNNING` | 3 |
| `TASK_STATUS_RUNNING_SUBTASKS` | 4 |
| `TASK_STATUS_SUCCESS` | 5 |
| `TASK_STATUS_FAILURE` | 6 |
| `TASK_STATUS_FAILED_UPSTREAM` | 7 |
| `TASK_STATUS_CANCELLED` | 8 |

### ShardScheduling

| Value | Number |
|-------|--------|
| `SHARD_SCHEDULING_UNSPECIFIED` | 0 |
| `SHARD_SCHEDULING_STANDARD` | 1 |
| `SHARD_SCHEDULING_GANG` | 2 |

