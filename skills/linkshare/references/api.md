# Linkshare API

Use this reference when debugging the bundled client or extending its behavior.

## Configuration

The client resolves the service origin in this order:

1. `LINKSHARE_URL`
2. `url` in `~/.config/linkshare/config.json`

Optional configuration keys:

```json
{
  "url": "http://linkshare-host:8080",
  "default_actor": "codex-agent"
}
```

## Endpoints

### Create a link

`POST /api/v1/links`

Required fields:

- `url`: HTTP or HTTPS URL
- `target`: `owner` or `agents`
- `submitted_by`: free-form actor name

Optional fields: `title`, `note`.

New links are unread. A successful request returns HTTP 201 and the created link.

### List links

`GET /api/v1/links`

Query fields:

- `target`: required, `owner` or `agents`
- `state`: `active`, `unread`, `read`, `archived`, or `all`
- `limit`: 1–200
- `before_id`: pagination cursor

The response contains `items`, `total`, and `next_before_id`.

### Change state

`PATCH /api/v1/links/{id}`

Body fields:

- `action`: `mark_read`, `mark_unread`, `archive`, or `restore`
- `actor`: free-form actor name

Archiving preserves the read receipt. Restoring returns the link to its prior read state.

## Errors

Errors use:

```json
{
  "error": {
    "code": "stable_code",
    "message": "Human-readable explanation"
  }
}
```

Common statuses:

- `400`: malformed JSON or ID
- `403`: cross-origin browser request denied
- `404`: link not found
- `409`: invalid state transition
- `415`: JSON content type required
- `422`: validation failure
- `503`: database unavailable
