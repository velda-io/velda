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

