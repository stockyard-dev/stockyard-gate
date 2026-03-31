FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$(git describe --tags --always 2>/dev/null || echo dev)" \
    -o /bin/gate ./cmd/gate/

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /bin/gate /usr/local/bin/gate
ENV PORT=8780 \
    DATA_DIR=/data \
    GATE_UPSTREAM=http://localhost:3000 \
    GATE_ADMIN_KEY=changeme \
    GATE_RPM=60
EXPOSE 8780
ENTRYPOINT ["gate"]
