# Todoist Recipe Dashboard

A standalone Go web app for saving recipes (with uploaded photos) and pushing ingredients to Todoist.

Features:
- Create recipes from a web form (`name`, `photo`, `ingredients`).
- Use structured ingredient fields with optional measurements (for example `Chicken` + `500g`).
- Choose per ingredient whether it should be added to Todoist (defaults to adding all).
- Persist recipes in PostgreSQL.
- Store uploaded photos in `uploads/`.
- Push ingredients to Todoist from the UI or via QR scan.
- Generate QR codes per recipe (`/qr/{id}`) that open `/scan/{id}`.
- Archive recipes from the dashboard with a confirmation prompt (soft delete).

## Requirements

- Go 1.22+
- PostgreSQL 13+
- A Todoist API token (for push actions)

## Setup

1) Clone/open this project and install dependencies:

```bash
go mod tidy
```

2) Export environment variables in your shell:

```bash
export DATABASE_URL="postgres://user:password@postgres:5432/todoist_recipes?sslmode=disable"
export TODOIST_API_TOKEN="your_todoist_token_here"
export TODOIST_PROJECT_ID=""
export TODOIST_API_BASE_URL="https://api.todoist.com/api/v1"
export BASE_URL=""
export LOCAL_IP="localhost"
```

`BASE_URL` is preferred for QR links. `LOCAL_IP` is kept for compatibility.
`DATABASE_URL` is required and must point to your PostgreSQL instance.
`TODOIST_PROJECT_ID` is optional; when set, created tasks are added to that specific Todoist project.
`TODOIST_API_BASE_URL` is optional and defaults to `https://api.todoist.com/api/v1`.

3) Run the app:

```bash
go run .
```

4) Open:

- `http://localhost:8080`

## QR URL Configuration (Important)

QR codes must point to a URL your phone can reach.

You can configure that with flags or env vars.

Flags:
- `--base-url` full public URL, e.g. `http://192.168.1.42:8080` or `https://example.ngrok.app`
- `--local-ip` host/IP (optionally with port), URL auto-generated
- `--addr` listen address for server (default `:8080`)
- `--todoist-project` Todoist project ID used for created tasks

Examples:

```bash
# Preferred: explicit full URL
go run . --base-url "http://192.168.1.42:8080"

# Host only: app infers scheme/port
go run . --local-ip "192.168.1.42"

# Custom listening port + inferred base URL port
go run . --addr ":9090" --local-ip "192.168.1.42"

# Env var mode
BASE_URL="https://my-tunnel-url.ngrok.app" go run .

# Send tasks to a specific Todoist project
go run . --todoist-project "1234567890"

# Env var mode for project selection
TODOIST_PROJECT_ID="1234567890" go run .

# Override Todoist API base (advanced)
TODOIST_API_BASE_URL="https://api.todoist.com/api/v1" go run .
```

Resolution priority for QR target base URL:
1. `--base-url`
2. `--local-ip`
3. `BASE_URL`
4. `LOCAL_IP`
5. Request host fallback

## Todoist Project Selection

By default, tasks are created in your Todoist inbox/default project behavior.

If you want all pushed ingredients to go to a specific project, set either:
- `--todoist-project "<project_id>"`
- `TODOIST_PROJECT_ID=<project_id>` in your shell

Project selection priority:
1. `--todoist-project`
2. `TODOIST_PROJECT_ID`
3. `TODOIST_PROJECT` (legacy compatibility)

How to find the project ID:
- In Todoist web app, open the project and inspect the URL.
- Or use Todoist API/tools to list projects and copy the `id`.

On startup, when a project ID is configured, the app validates the project against Todoist.
If validation fails, the app exits early with a clear error.

## Routes

- `GET /` dashboard + create form
- `POST /api/recipes` create recipe (multipart upload)
- `POST /api/recipes/{id}/delete` archive recipe (soft delete)
- `POST /api/push/{id}` push recipe ingredients to Todoist
- `GET /scan/{id}` mobile-friendly push endpoint for QR scans
- `GET /recipes/{id}/qr` printable QR page with recipe name
- `GET /qr/{id}` QR code PNG for recipe
- `GET /uploads/{file}` uploaded image files

## Notes for Running on Your Network

- If you want to scan from your phone, run the app with a LAN-reachable base URL.
- Ensure your phone and computer are on the same network.
- Open firewall for the app port if needed (default `8080`).
- If using HTTPS tunnel services (ngrok/cloudflared), set `BASE_URL` to the HTTPS URL.

## Data and Files

- Recipes are stored in PostgreSQL table `recipes`.
- Uploaded images are stored in `uploads/`.
- Runtime configuration is read directly from process environment variables.

Soft-delete behavior:
- Deleting from the UI sets `recipes.deleted_at` and hides the recipe from the dashboard.
- Archived recipes are retained in PostgreSQL for recovery.
- Image files are not removed during archive, so restored recipes keep their photos.

Recover archived recipes manually:

```sql
-- Find archived recipes
SELECT id, name, deleted_at
FROM recipes
WHERE deleted_at IS NOT NULL
ORDER BY deleted_at DESC;

-- Restore one recipe
UPDATE recipes
SET deleted_at = NULL
WHERE id = 'your_recipe_id';
```

## Build Binary

```bash
go build ./...
```

The binary name will be based on module/package (currently `todoist-recipes`).

## Docker

Build image:

```bash
make docker-build
```

Run container (reads env vars from your shell, persists `uploads/`):

```bash
mkdir -p uploads
make docker-run
```

### Docker Compose (App + PostgreSQL)

This is the easiest local setup that mirrors a Kubernetes-style split between app and database.

Start both services:

```bash
make compose-up
```

View logs:

```bash
make compose-logs
```

Stop services:

```bash
make compose-down
```

Useful environment overrides before `make compose-up`:

```bash
export TODOIST_API_TOKEN="your_todoist_token_here"
export POSTGRES_DB="todoist_recipes"
export POSTGRES_USER="todoist"
export POSTGRES_PASSWORD="todoist"
export PORT="8080"
```

Compose persistence:
- PostgreSQL data is stored in volume `pg_data`.
- Uploaded recipe images are stored in volume `uploads_data`.

Custom image/port examples:

```bash
make docker-build IMAGE=todoist-recipes:dev
make docker-run IMAGE=todoist-recipes:dev PORT=9090
```

Stop running containers for this image:

```bash
make docker-stop
```

## Troubleshooting

- `TODOIST_API_TOKEN is not set`
  - Export `TODOIST_API_TOKEN` in your shell before starting the app.
- `DATABASE_URL is not set`
  - Export `DATABASE_URL` in your shell before starting the app.
- Tasks not appearing in expected project
  - Set `--todoist-project` or `TODOIST_PROJECT_ID` to the correct project ID.
- `todoist status 410: This endpoint is deprecated`
  - Upgrade to this version of the app (it uses `/api/v1` endpoints by default).
  - If needed, set `TODOIST_API_BASE_URL` explicitly to `https://api.todoist.com/api/v1`.
- QR scan opens but push fails
  - Verify token validity and internet access from the machine running the app.
- QR scan cannot connect
  - Fix `--base-url`/`BASE_URL` so it points to a reachable URL from your phone.
