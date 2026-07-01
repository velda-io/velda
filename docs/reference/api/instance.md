---
title: Instance
---

# Instance

### Create Instance

`PUT /rest/instances`

**Authorization**: Bearer `<token>`

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `instance` | [Instance](#instance) |  |
| `image_name` | string | _(oneof source)_ |
| `snapshot` | [SnapshotReference](#snapshotreference) | _(oneof source)_ |
| `region` | string | Optional: specify region by name  If not specified, uses the current region.  Only used for hosted / enterprise. |
| `docker_image` | string | Optional: if set, the server will enqueue a remote initialization task  that imports this container image into the newly created instance. |
| `skip_docker_init_script` | bool | Optional: skip the post-install initialization script when  docker_image is set. |
| `docker_auth_username` | string | Optional: registry username for private docker_image pulls.  Used only when docker_image is set. |
| `docker_auth_password` | string | Optional: registry password for private docker_image pulls.  Used only when docker_image is set. |

**Response** (200 OK)

`application/json` — [Instance](#instance)

**Example**

```http
PUT /rest/instances HTTP/1.1
Authorization: Bearer <token>
```

---

### Get Instance

`GET /rest/instances/{instance_id}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `instance_id` | int64 |  |

**Response** (200 OK)

`application/json` — [Instance](#instance)

**Example**

```http
GET /rest/instances/{instance_id} HTTP/1.1
Authorization: Bearer <token>
```

---

### Get Instance By Name

`GET /rest/instances/by_name/{instance_name}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `instance_name` | string |  |

**Response** (200 OK)

`application/json` — [Instance](#instance)

**Example**

```http
GET /rest/instances/by_name/{instance_name} HTTP/1.1
Authorization: Bearer <token>
```

---

### Delete Instance

`DELETE /rest/instances/{instance_id}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `instance_id` | int64 |  |

**Response** (200 OK)

Empty response.

**Example**

```http
DELETE /rest/instances/{instance_id} HTTP/1.1
Authorization: Bearer <token>
```

---

### List Instances

`GET /rest/instances`

**Authorization**: Bearer `<token>`

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `page_size` | int32 |  |
| `page_token` | string |  |

**Response** (200 OK)

`application/json` — [ListInstancesResponse](#listinstancesresponse)

**Example**

```http
GET /rest/instances HTTP/1.1
Authorization: Bearer <token>
```

---

### Create Snapshot

`PUT /rest/instances/{instance_id}/snapshot/{snapshot_name}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `instance_id` | int64 |  |
| `snapshot_name` | string |  |

**Response** (200 OK)

`application/json` — [SnapshotReference](#snapshotreference)

**Example**

```http
PUT /rest/instances/{instance_id}/snapshot/{snapshot_name} HTTP/1.1
Authorization: Bearer <token>
Content-Type: application/json
```

---

### Delete Snapshot

`DELETE /rest/instances/{instance_id}/snapshot/{snapshot_name}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `instance_id` | int64 |  |
| `snapshot_name` | string |  |

**Response** (200 OK)

Empty response.

**Example**

```http
DELETE /rest/instances/{instance_id}/snapshot/{snapshot_name} HTTP/1.1
Authorization: Bearer <token>
```

---

### List Images

`GET /rest/images`

**Authorization**: Bearer `<token>`

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `prefix` | string |  |
| `region` | string |  |

**Response** (200 OK)

`application/json` — [ListImagesResponse](#listimagesresponse)

**Example**

```http
GET /rest/images HTTP/1.1
Authorization: Bearer <token>
```

---

### Create Image

`PUT /rest/images`

**Authorization**: Bearer `<token>`

**Request Body** (application/json)

| Field  &nbsp; &nbsp; &nbsp; &nbsp; &nbsp; | Type | Description |
|-------|------|-------------|
| `image_name` | string |  |
| `instance_id` | int64 |  |
| `snapshot_name` | string |  |

**Response** (200 OK)

Empty response.

**Example**

```http
PUT /rest/images HTTP/1.1
Authorization: Bearer <token>
Content-Type: application/json
```

---

### Delete Image

`DELETE /rest/images/{image_name}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `image_name` | string |  |

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `region` | string |  |

**Response** (200 OK)

Empty response.

**Example**

```http
DELETE /rest/images/{image_name} HTTP/1.1
Authorization: Bearer <token>
```

---

### List Regions

`GET /velda/regions`

**Authorization**: Bearer `<token>`

**Response** (200 OK)

`application/json` — [ListRegionsResponse](#listregionsresponse)

**Example**

```http
GET /velda/regions HTTP/1.1
Authorization: Bearer <token>
```

---

## Message Types

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

### ListImagesResponse

| Field | Type | Description |
|-------|------|-------------|
| `images` | string[] |  |

### ListInstancesResponse

| Field | Type | Description |
|-------|------|-------------|
| `instances` | [Instance](#instance)[] |  |
| `next_page_token` | string |  |

### ListRegionsResponse

| Field | Type | Description |
|-------|------|-------------|
| `regions` | string[] |  |
| `region_display_infos` | [RegionDisplayInfo](#regiondisplayinfo)[] |  |
| `current_region` | string |  |

### RegionDisplayInfo

| Field | Type | Description |
|-------|------|-------------|
| `name` | string |  |
| `logo` | [RegionLogo](#regionlogo) |  |

### SnapshotReference

| Field | Type | Description |
|-------|------|-------------|
| `instance_id` | int64 |  |
| `snapshot_name` | string |  |

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

