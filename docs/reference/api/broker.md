---
title: Broker
---

# Broker

The broker service.

### Kill Session

`POST /rest/broker/v1/session/kill`

**Authorization**: Bearer `<token>`

**Request Body** (application/json)

| Field  &nbsp; &nbsp; &nbsp; &nbsp; &nbsp; | Type | Description |
|-------|------|-------------|
| `instance_id` | int64 |  |
| `session_id` | string |  |
| `service_name` | string |  |
| `force` | bool | If true, will kill through Cgroup's kill switch.  If false, will send a SIGTERM to the main daemon. |

**Response** (200 OK)

Empty response.

**Example**

```http
POST /rest/broker/v1/session/kill HTTP/1.1
Authorization: Bearer <token>
Content-Type: application/json
```

---

### List Sessions

`GET /rest/broker/v1/sessions`

**Authorization**: Bearer `<token>`

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `instance_id` | int64 |  |
| `service_name` | string |  |

**Response** (200 OK)

`application/json` — [ListSessionsResponse](#listsessionsresponse)

**Example**

```http
GET /rest/broker/v1/sessions HTTP/1.1
Authorization: Bearer <token>
```

---

### Set Tag

`POST /rest/broker/v1/session/set-tag`

**Authorization**: Bearer `<token>`

**Request Body** (application/json)

| Field  &nbsp; &nbsp; &nbsp; &nbsp; &nbsp; | Type | Description |
|-------|------|-------------|
| `instance_id` | int64 |  |
| `session_id` | string |  |
| `tags` | map&lt;string, string&gt; | Tags to set or update. If a tag value is empty, the tag will be removed. |

**Response** (200 OK)

`application/json` — [SetTagResponse](#settagresponse)

**Example**

```http
POST /rest/broker/v1/session/set-tag HTTP/1.1
Authorization: Bearer <token>
Content-Type: application/json
```

---

## Message Types

### ListSessionsResponse

| Field | Type | Description |
|-------|------|-------------|
| `sessions` | [Session](#session)[] |  |

### Session

| Field | Type | Description |
|-------|------|-------------|
| `session_id` | string |  |
| `user` | string |  |
| `pool` | string |  |
| `instance_id` | int64 |  |
| `status` | [Status](#status) |  |
| `service_name` | string |  |
| `labels` | string[] |  |
| `internal_ip_address` | string |  |
| `tags` | map&lt;string, string&gt; | Key-value tags for the session. |

### SetTagResponse

_(empty message)_

### Status

| Value | Number |
|-------|--------|
| `STATUS_UNSPECIFIED` | 0 |
| `STATUS_QUEUEING` | 1 |
| `STATUS_PENDING` | 2 |
| `STATUS_RUNNING` | 3 |
| `STATUS_TERMINATED` | 4 |
| `STATUS_STALE` | 5 |
| `STATUS_CHECKPOINTED` | 6 |
| `STATUS_WAITING_FOR_OTHER_SHARDS` | 7 |
| `STATUS_CANCELLED` | 8 |

