---
title: Image Management Ent
---

# Image Management Ent

ImageManagementEnt provides enterprise-only image management endpoints

### Search Images

`GET /rest/images/search`

**Authorization**: Bearer `<token>`

**Query Parameters**

| Parameter | Type | Description |
|-----------|------|-------------|
| `query` | string |  |
| `limit` | int32 |  |
| `offset` | int32 |  |

**Response** (200 OK)

`application/json` — [SearchImagesResponse](#searchimagesresponse)

**Example**

```http
GET /rest/images/search HTTP/1.1
Authorization: Bearer <token>
```

---

## Message Types

### Image

| Field | Type | Description |
|-------|------|-------------|
| `id` | string |  |
| `slug` | string |  |
| `name` | string |  |
| `description` | string |  |
| `github_link` | string |  |
| `latest_image_id` | string |  |
| `cover_photo` | string |  |
| `creation_count` | int64 |  |
| `created_at` | string (RFC 3339) |  |
| `updated_at` | string (RFC 3339) |  |
| `region` | string |  |
| `owner` | [User](#user) | owner is kept for internal use only and is not exposed in public API  responses. |
| `is_public` | bool |  |
| `owner_org_name` | string |  |
| `owner_org_id` | int64 |  |

### SearchImagesResponse

| Field | Type | Description |
|-------|------|-------------|
| `images` | [Image](#image)[] |  |

### User

| Field | Type | Description |
|-------|------|-------------|
| `email` | string |  |
| `display_name` | string |  |
| `avatar` | string |  |
| `org_id` | int64 |  |
| `created_at` | string (RFC 3339) |  |
| `role` | [Role](#role) |  |
| `first_name` | string |  |
| `last_name` | string |  |
| `ga_client_id` | string | Google Analytics client ID (from _ga cookie), stored for server-side  attribution. |
| `id` | int64 | Internal user ID (opaque integer, non-PII) used for GA user_id tracking. |

### Role

| Value | Number |
|-------|--------|
| `ROLE_UNSPECIFIED` | 0 |
| `ROLE_SITE_ADMIN` | 1 |
| `ROLE_ADMIN` | 2 |
| `ROLE_MANAGER` | 3 |
| `ROLE_USER` | 4 |
| `ROLE_SUSPENDED` | 5 |
| `ROLE_INVITED` | 6 |
| `ROLE_WAITING_LIST` | 7 |
| `ROLE_TOS_UNACCEPTED` | 8 |
| `ROLE_UNVERIFIED` | 9 |

