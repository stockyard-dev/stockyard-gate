FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.version=$(git describe --tags --always 2>/dev/null || echo dev)" \
    -o /bin/gate ./cmd/gate/

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata curl

COPY --from=builder /bin/gate /usr/local/bin/gate

# Environment variables — override at runtime
# DATA_DIR should be backed by a persistent volume in production
ENV PORT="8780" \
    DATA_DIR="/data" \
    GATE_UPSTREAM="http://localhost:3000" \
    GATE_ADMIN_KEY="changeme" \
    GATE_RPM="60" \
    GATE_CORS_ORIGINS="" \
    GATE_LICENSE_KEY=""

EXPOSE 8780

# Healthcheck — adjust interval for your use case
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -sf http://localhost:8780/health || exit 1

ENTRYPOINT ["gate"]
