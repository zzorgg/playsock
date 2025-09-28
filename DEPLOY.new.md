## Deploying on DigitalOcean App Platform (Managed Valkey)

Only App Platform is supported here. No Droplets, no manual servers.

### 1. Push Repository (if not already)
```bash
git init
git add .
git commit -m "deploy"
git branch -M maingit remote add origin https://github.com/yourusername/playsock.git
git push -u origin main
```

### 2. Create the App
1. Go to https://cloud.digitalocean.com/apps
2. Create App → pick the GitHub repo
3. Component: Web Service
4. Root Directory: `/`
5. Runtime: Dockerfile (auto-detected)
6. HTTP Port: 8080
7. WebSocket Path: `/ws`

No health check endpoint exists (`/healthz` was removed). Leave health check blank.

### 3. (Optional) Add Managed Valkey
1. Add Resource → Database → Valkey
2. Select a plan (dev tier fine for testing)
3. Attach it to the Web Service
4. App Platform will expose connection values (`${valkey.HOSTNAME}` etc.)

If omitted, the server uses in‑memory queue/sessions (only suitable for a single instance).

### 4. Environment Variables

| Key | Example | Purpose |
|-----|---------|---------|
| `PLAYSOCK_ALLOWED_ORIGINS` | `playsock-mobile://app` | Comma list of allowed origins; avoid `*` unless clients send none. |
| `PLAYSOCK_QUEUE_TIMEOUT` | `5m` | Max time a player waits before removal. |
| `PLAYSOCK_VALKEY_ADDR` | `${valkey.HOSTNAME}:${valkey.PORT}` | Host:port for Valkey cluster. Leave unset to disable Valkey. |
| `PLAYSOCK_VALKEY_USERNAME` | `${valkey.USERNAME}` | If your Valkey instance uses ACL auth. |
| `PLAYSOCK_VALKEY_PASSWORD` | `${valkey.PASSWORD}` | Password / token. |
| `PLAYSOCK_VALKEY_DB` | `0` | Database index (integer). |
| `PLAYSOCK_VALKEY_TIMEOUT` | `2s` | Timeout per Valkey operation. |
| `PLAYSOCK_SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown drain window. |

Legacy `PLAYSOCK_REDIS_*` names still work temporarily; migrate to `PLAYSOCK_VALKEY_*` only.

### 5. Deploy
Click Deploy. First build can take a couple minutes while dependencies download.

### 6. Connect
Your WebSocket endpoint format:
```
wss://<app-name>-<hash>.ondigitalocean.app/ws
```
Join queue example frame:
```json
{"action":"join_queue","data":{"player_id":"alice","display_name":"Alice"}}
```

### 7. Scaling
Increase instance count in App Platform. With Valkey enabled, multiple instances coordinate queue & session state.

### 8. Security
1. Restrict `PLAYSOCK_ALLOWED_ORIGINS` to production origins.
2. Store Valkey credentials only in App Platform env vars.
3. Periodically redeploy to get base image & Go updates.
4. Tune `PLAYSOCK_VALKEY_TIMEOUT` for slow network scenarios.

### 9. Disable Valkey
Unset `PLAYSOCK_VALKEY_ADDR` to run single-instance in-memory only (for local/testing, not production).

---
Future ideas: metrics, structured logs, match history export.
