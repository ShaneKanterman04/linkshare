# Linkshare API

Use this reference when debugging the bundled client or extending its behavior.

## Configuration

The client resolves the service origin from `LINKSHARE_URL`, then `url` in `~/.config/linkshare/config.json`. `default_actor` supplies the sender name.

## Endpoints

### Discover

`GET /api/v1` returns the service metadata, expiry policy, and every supported endpoint with a short description.

### Create

`POST /api/v1/links`

Required JSON fields: `url`, `target` (`owner` or `agents`), and `submitted_by`. Optional fields: `title` and `note`. HTTP 201 returns the link including `expires_at`.

### List

`GET /api/v1/links`

Query fields: required `target`, optional `limit` (1–200), and optional `before_id`. Results contain only unexpired links and include `items`, `total`, and `next_before_id`.

### Consume

`DELETE /api/v1/links/{id}` permanently removes a link and returns HTTP 204. Consume only after successful use.

For older clients, `state=active` and `state=unread` remain accepted. Legacy `mark_read` and `archive` PATCH actions consume the item.

## Errors

Errors contain a stable `error.code` and human-readable `error.message`. Common statuses are 400 for malformed input, 403 for denied cross-origin requests, 404 for missing or expired links, 415 for an invalid content type, 422 for validation errors, and 503 for database unavailability.
