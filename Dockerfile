# check=skip=SecretsUsedInArgOrEnv
# Build stage
FROM golang:1.24-alpine3.21 AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /rdp-broker ./cmd/broker

# Runtime stage - Debian sid has freerdp-proxy package
FROM debian:sid-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        freerdp-proxy \
        openssl \
        curl \
        procps \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /rdp-broker /usr/local/bin/rdp-broker

RUN mkdir -p /tmp/sessions /etc/rdp-broker/certs && \
    chmod 700 /tmp/sessions

# Run as root for dev simplicity (use non-root user in production)
# RUN useradd -r -s /bin/false rdpbroker && \
#     chown -R rdpbroker:rdpbroker /tmp/sessions /etc/rdp-broker
# USER rdpbroker

EXPOSE 8080 33400-33500

ENV API_PORT=8080 \
    BROKER_HOST=localhost \
    BROKER_DOMAIN=YOURORG \
    PROXY_PORT_START=33400 \
    PROXY_PORT_END=33500 \
    PROXY_INTERNAL_OFFSET=11000 \
    CREDENTIAL_PROVIDER=mock \
    CERT_DIR=/etc/rdp-broker/certs \
    SESSION_DIR=/tmp/sessions \
    FREERDP_PROXY_BIN=freerdp-proxy \
    MAX_CONCURRENT_SESSIONS=100 \
    SESSION_MAX_DURATION=8h \
    TOKEN_TTL=60s \
    LOG_LEVEL=info \
    LOG_FORMAT=json

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

ENTRYPOINT ["/usr/local/bin/rdp-broker"]
