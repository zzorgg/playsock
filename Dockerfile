# Build stage
FROM golang:1.25-alpine AS builder

ENV CGO_ENABLED=0

WORKDIR /app

# Copy go mod files first to leverage layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the remaining source code and build the binary
COPY . .
RUN go build -trimpath -ldflags "-s -w" -o /out/playsock ./

# Runtime stage
FROM alpine:3.20

# Install runtime dependencies and create an unprivileged user
RUN apk --no-cache add ca-certificates wget \
    && addgroup -g 1001 -S appuser \
    && adduser -u 1001 -S appuser -G appuser

WORKDIR /app

# Copy the statically linked binary
COPY --from=builder --chown=appuser:appuser /out/playsock ./playsock

USER appuser

EXPOSE 8080

# Simple container healthcheck for orchestration platforms
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD wget -q -O- http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/app/playsock"]