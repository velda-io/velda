---
title: Pool Manager
---

# Pool Manager

### List Pools

`GET /rest/pool`

**Authorization**: Bearer `<token>`

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `region` | string |  |

**Response** (200 OK)

`application/json` — [ListPoolsResponse](#listpoolsresponse)

**Example**

```http
GET /rest/pool HTTP/1.1
Authorization: Bearer <token>
```

---

### Get Pool

`GET /rest/pool/{pool}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `pool` | string |  |

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `region` | string |  |

**Response** (200 OK)

`application/json` — [GetPoolResponse](#getpoolresponse)

**Example**

```http
GET /rest/pool/{pool} HTTP/1.1
Authorization: Bearer <token>
```

---

### Watch Pool Status

`GET /rest/pool/{pool}/watch` _(server-streaming)_

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `pool` | string |  |

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `region_id` | int32 |  |

**Response** (200 OK)

`application/json` — [PoolStatusNotification](#poolstatusnotification)

**Example**

```http
GET /rest/pool/{pool}/watch HTTP/1.1
Authorization: Bearer <token>
```

---

## Message Types

### GetPoolResponse

| Field | Type | Description |
|-------|------|-------------|
| `pool` | [Pool](#pool) |  |

### ListPoolsResponse

| Field | Type | Description |
|-------|------|-------------|
| `pools` | [Pool](#pool)[] |  |

### Pool

| Field | Type | Description |
|-------|------|-------------|
| `name` | string |  |
| `description` | string |  |
| `autoscaler_status` | [PoolAutoscalerStatus](#poolautoscalerstatus) |  |
| `price_micros_per_hour` | int32 |  |

### PoolAutoscalerStatus

| Field | Type | Description |
|-------|------|-------------|
| `last_allocation_error` | string |  |
| `last_allocation_error_time` | string (RFC 3339) |  |

### PoolStatusNotification

| Field | Type | Description |
|-------|------|-------------|
| `pool` | string |  |
| `autoscaler_status` | [PoolAutoscalerStatus](#poolautoscalerstatus) |  |

