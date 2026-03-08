# Multi-stage build for minimal production image.
# Stage 1: compile a static binary with no CGO dependencies.
# Stage 2: copy into a scratch-based Alpine for TLS certs + shell access.
FROM golang:1.21-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w -X main.version=2.0.0" \
    -o /edge-cdn ./cmd/router

# --- Runtime ---
FROM alpine:3.19

RUN apk add --no-cache ca-certificates curl
COPY --from=builder /edge-cdn /usr/local/bin/edge-cdn

# Non-root user for security.
RUN adduser -D -u 1000 edgecdn
USER edgecdn

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

ENTRYPOINT ["edge-cdn"]
