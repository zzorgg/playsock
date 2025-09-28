# Deploying to DigitalOcean

There are two main ways to deploy your WebSocket server to DigitalOcean:

## Option 1: DigitalOcean App Platform (Recommended - Easiest)

### Prerequisites
- DigitalOcean account
- GitHub repository with your code

### Steps

1. **Push your code to GitHub** (if not already done):
   ```bash
   git init
   git add .
   git commit -m "Initial commit"
   git branch -M main
   git remote add origin https://github.com/yourusername/playsock.git
   git push -u origin main
   ```

2. **Create App on DigitalOcean**:
   - Go to [DigitalOcean App Platform](https://cloud.digitalocean.com/apps)
   - Click "Create App"
   - Connect your GitHub repository
   - Select the `playsock` repository

3. **Configure the App**:
   - **Service Type**: Web Service
   - **Source Directory**: `/` (root)
   - **Environment**: Dockerfile
   - **HTTP Port**: 8080
   - **Health Check Path**: `/healthz`
   - **Health Check Protocol/Port**: HTTP on 8080

4. **Add Redis Database** (Optional but recommended):
   - In the app configuration, click "Add Resource"
   - Select "Database" → "Redis"
   - Choose plan (Dev plan is free)

5. **Environment Variables** (suggested defaults):

   | Key | Example | Notes |
   |-----|---------|-------|
   | `PLAYSOCK_ALLOWED_ORIGINS` | `playsock-mobile://app` | List the exact origins your mobile clients emit (custom schemes or `https://` domains). Use `*` only if mobile libraries omit the header entirely. |
   | `PLAYSOCK_QUEUE_TIMEOUT` | `5m` | Increase if you anticipate longer matchmaking windows. |
   | `PLAYSOCK_REDIS_ADDR` | `${redis.HOSTNAME}:${redis.PORT}` | Provided automatically when you attach a managed Redis instance. |
   | `PLAYSOCK_REDIS_PASSWORD` | `${redis.PASSWORD}` | Required when using managed Redis. |
   | `PLAYSOCK_SHUTDOWN_TIMEOUT` | `15s` | Optional, allows App Platform graceful shutdowns to drain connections. |

6. **Deploy**:
   - Review configuration
   - Click "Create Resources"
   - Wait for deployment (5-10 minutes)

### Your WebSocket URL will be:
```
wss://your-app-name-xxxxx.ondigitalocean.app/ws
```

## Option 2: DigitalOcean Droplet (More Control)

### Prerequisites
- DigitalOcean account
- Basic Linux knowledge

### Steps

1. **Create Droplet**:
   - Go to DigitalOcean → Droplets → Create
   - Choose Ubuntu 22.04 LTS
   - Select size (Basic $6/month is sufficient)
   - Add SSH key or use password

2. **Connect to Droplet**:
   ```bash
   ssh root@your-droplet-ip
   ```

3. **Install Docker & Docker Compose**:
   ```bash
   # Update system
   apt update && apt upgrade -y
   
   ## Deploying to DigitalOcean App Platform

   This project is optimized for DigitalOcean App Platform using its Docker build.

   ### 1. Push Repository
   If not yet pushed:
   ```bash
   git init
   git add .
   git commit -m "Deploy"
   git branch -M main
   git remote add origin https://github.com/yourusername/playsock.git
   git push -u origin main
   ```

   ### 2. Create App
   1. Visit App Platform
   2. Create App → choose the GitHub repo
   3. Select root directory `/`
   4. Component type: Web Service
   5. Port: 8080 (the container exposes it)

   ### 3. Optional Valkey (Managed Database)
   Add a Managed Database → Valkey. This enables multi-instance coordination for queue & session metadata.

   ### 4. Environment Variables

   | Key | Example | Notes |
   |-----|---------|-------|
   | `PLAYSOCK_ALLOWED_ORIGINS` | `playsock-mobile://app` | Restrict to your mobile / web origins. Use `*` only if clients do not send Origin. |
   | `PLAYSOCK_QUEUE_TIMEOUT` | `5m` | Max time a player can sit in the queue. |
   | `PLAYSOCK_VALKEY_ADDR` | `${valkey.HOSTNAME}:${valkey.PORT}` | Auto-provided when attaching a Valkey managed database. Leave unset to disable. |
   | `PLAYSOCK_VALKEY_PASSWORD` | `${valkey.PASSWORD}` | Required for managed Valkey if auth enabled. |
   | `PLAYSOCK_VALKEY_TIMEOUT` | `2s` | Operation timeout (duration string). |
   | `PLAYSOCK_SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown drain window. |

   Legacy `PLAYSOCK_REDIS_*` variables are still accepted but will be removed later—migrate to `PLAYSOCK_VALKEY_*`.

   ### 5. Deploy
   Click Deploy. First build typically finishes in a few minutes.

   ### 6. Connect From Client
   Your WebSocket endpoint:
   ```
   wss://<app-name>-<hash>.ondigitalocean.app/ws
   ```

   Send a sample frame:
   ```json
   {"action":"join_queue","data":{"player_id":"alice","display_name":"Alice"}}
   ```

   ### Logs & Scaling
   Use the App Platform dashboard → Logs. Scale vertically or add more instances; with Valkey configured state coordination works across replicas.

   ### Security Notes
   1. Pin exact origins in `PLAYSOCK_ALLOWED_ORIGINS`.
   2. Keep Valkey credentials only in App Platform env vars.
   3. Rebuild periodically for patched base image & Go runtime.
   4. Set sensible timeouts via `PLAYSOCK_VALKEY_TIMEOUT` & `PLAYSOCK_SHUTDOWN_TIMEOUT`.

   ### Removing Valkey
   Leave `PLAYSOCK_VALKEY_ADDR` empty to run purely in-memory (single instance only).

   ---
   Future enhancements: add metrics, structured logging, and optional rate limit tuning.