---
title: Volume
---

# Volume

### Create Volume

`POST /velda/volumes`

**Authorization**: Bearer `<token>`

**Request Body** (application/json)

| Field  &nbsp; &nbsp; &nbsp; &nbsp; &nbsp; | Type | Description |
|-------|------|-------------|
| `volume` | [Volume](#volume) |  |
| `region` | string |  |

**Response** (200 OK)

`application/json` — [Volume](#volume)

**Example**

```http
POST /velda/volumes HTTP/1.1
Authorization: Bearer <token>
Content-Type: application/json
```

---

### Get Volume

`GET /velda/volumes/{volume_id}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `volume_id` | int64 |  |

**Response** (200 OK)

`application/json` — [Volume](#volume)

**Example**

```http
GET /velda/volumes/{volume_id} HTTP/1.1
Authorization: Bearer <token>
```

---

### List Volumes

`GET /velda/volumes`

**Authorization**: Bearer `<token>`

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `page_size` | int32 |  |
| `page_token` | string |  |

**Response** (200 OK)

`application/json` — [ListVolumesResponse](#listvolumesresponse)

**Example**

```http
GET /velda/volumes HTTP/1.1
Authorization: Bearer <token>
```

---

### Delete Volume

`DELETE /velda/volumes/{volume_id}`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `volume_id` | int64 |  |

**Response** (200 OK)

Empty response.

**Example**

```http
DELETE /velda/volumes/{volume_id} HTTP/1.1
Authorization: Bearer <token>
```

---

### Update Volume Size

`PATCH /velda/volumes/{volume_id}/size`

**Authorization**: Bearer `<token>`

**Path Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `volume_id` | int64 |  |

**Request Body** (application/json)

| Field  &nbsp; &nbsp; &nbsp; &nbsp; &nbsp; | Type | Description |
|-------|------|-------------|
| `size_gb` | int32 | New volume size in GB. Can only be increased. |

**Response** (200 OK)

Empty response.

**Example**

```http
PATCH /velda/volumes/{volume_id}/size HTTP/1.1
Authorization: Bearer <token>
Content-Type: application/json
```

---

## Message Types

### ListVolumesResponse

| Field | Type | Description |
|-------|------|-------------|
| `volumes` | [Volume](#volume)[] |  |
| `next_page_token` | string |  |

### Volume

| Field | Type | Description |
|-------|------|-------------|
| `id` | int64 |  |
| `name` | string |  |
| `org_id` | int64 | org_id is the organization this volume belongs to. |
| `status` | [VolumeStatus](#volumestatus) |  |
| `size_gb` | int32 | Volume size in GB. |
| `region` | string |  |

### VolumeStatus

| Value | Number |
|-------|--------|
| `VOLUME_STATUS_UNSPECIFIED` | 0 |
| `VOLUME_STATUS_ACTIVE` | 1 |
| `VOLUME_STATUS_TERMINATED` | 2 |

