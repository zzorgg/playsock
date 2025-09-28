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
   - **Build Command**: Leave empty (Docker will handle it)
   - **Run Command**: Leave empty (Docker will handle it)
   - **Port**: 8080
   - **HTTP Port**: 8080

4. **Add Redis Database** (Optional but recommended):
   - In the app configuration, click "Add Resource"
   - Select "Database" → "Redis"
   - Choose plan (Dev plan is free)

5. **Environment Variables**:
   - Add `PLAYSOCK_ALLOWED_ORIGINS` = `*` (or your domain)
   - If using Redis database, add `PLAYSOCK_REDIS_ADDR` = `${redis.HOSTNAME}:${redis.PORT}`

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
   
   # Install Docker
   curl -fsSL https://get.docker.com -o get-docker.sh
   sh get-docker.sh
   
   # Install Docker Compose
   apt install docker-compose -y
   
   # Start Docker
   systemctl start docker
   systemctl enable docker
   ```

4. **Deploy your application**:
   ```bash
   # Clone your repository
   git clone https://github.com/yourusername/playsock.git
   cd playsock
   
   # Build and run with Docker Compose
   docker-compose up -d
   ```

5. **Configure Firewall**:
   ```bash
   # Install UFW if not installed
   apt install ufw -y
   
   # Allow SSH, HTTP, and your app port
   ufw allow ssh
   ufw allow 80
   ufw allow 443
   ufw allow 8080
   ufw --force enable
   ```

6. **Setup Domain (Optional)**:
   - Point your domain to the droplet IP
   - Install nginx for reverse proxy:
   ```bash
   apt install nginx -y
   ```
   
   Create nginx config:
   ```bash
   cat > /etc/nginx/sites-available/playsock << 'EOF'
   server {
       listen 80;
       server_name your-domain.com;
       
       location /ws {
           proxy_pass http://localhost:8080;
           proxy_http_version 1.1;
           proxy_set_header Upgrade $http_upgrade;
           proxy_set_header Connection "upgrade";
           proxy_set_header Host $host;
           proxy_set_header X-Real-IP $remote_addr;
           proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
           proxy_set_header X-Forwarded-Proto $scheme;
       }
   }
   EOF
   
   # Enable site
   ln -s /etc/nginx/sites-available/playsock /etc/nginx/sites-enabled/
   nginx -t
   systemctl reload nginx
   ```

### Your WebSocket URL will be:
```
ws://your-droplet-ip:8080/ws
# or with domain:
ws://your-domain.com/ws
```

## Testing Deployment

Test your deployed server:

```bash
# Install wscat if not installed
npm install -g wscat

# Test connection
wscat -c wss://your-app-url/ws
# or
wscat -c ws://your-droplet-ip:8080/ws

# Send test message
{"action": "search_match"}
```

## Monitoring & Logs

### App Platform:
- View logs in DigitalOcean dashboard → Apps → Your App → Runtime Logs

### Droplet:
```bash
# View container logs
docker-compose logs -f playsock

# View system resources
htop
docker stats
```

## Cost Estimates

- **App Platform**: ~$5-12/month (includes hosting + Redis)
- **Droplet**: $6/month (Basic) + $15/month (Redis managed database, optional)

## Security Recommendations

1. **Use environment variables** for sensitive data
2. **Enable CORS** properly (don't use `*` in production)
3. **Use HTTPS/WSS** in production
4. **Regular updates** of dependencies
5. **Monitor logs** for suspicious activity

## Scaling

- **App Platform**: Auto-scales based on traffic
- **Droplet**: Manually resize or add load balancer for multiple droplets