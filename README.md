# Edge CDN

A production-grade, globally distributed Edge CDN built in Go. Designed for high throughput, low latency content delivery with a full resiliency stack.

## Architecture

```
                    +------------------+
                    |     Gateway      |
                    | (Geo-Router +    |
                    |  Load Balancer)  |
                    +--------+---------+
                             |
              +--------------+--------------+
              |              |              |
     +--------+------+ +----+--------+ +---+---------+
     | Edge Node      | | Edge Node   | | Edge Node   |
     | us-east        | | eu-west     | | ap-south    |
     |                | |             | |             |
     | +------------+ | |             | |             |
     | | LRU Cache  | | |             | |             |
     | +-----+------+ | |             | |             |
     |       |        | |             | |             |
     | +-----+------+ | |             | |             |
     | | Redis      | | |             | |             |
     | +------------+ | |             | |             |
     +--------+-------+ +------+------+ +------+------+
              |                |               |
              +----------------+---------------+
                               |
                    +----------+----------+
                    |   Origin Shield     |
                    | (Request Coalescing)|
                    +----------+----------+
                               |
                    +----------+----------+
                    |   Origin Server     |
                    +---------------------+
```

## Features

### Core
- **Consistent Hash Ring** -- xxhash-based routing ensures cache affinity across nodes
- **Two-Tier Caching** -- Redis primary with automatic LRU fallback when Redis is unavailable
- **Content-Type Detection** -- Automatic MIME type detection for HTML, JSON, CSS, JS, images
- **Cache Purge API** -- On-demand cache invalidation via POST endpoint

### Resiliency Stack
- **Origin Shield** -- Request coalescing via singleflight prevents thundering herd on cache misses
- **Cache Warming** -- Proactive prefetch of popular paths based on access frequency tracking
- **Circuit Breaker** -- Three-state (closed/open/half-open) protection against cascading origin failures
- **Rate Limiter** -- Per-IP token bucket rate limiting with configurable RPS and burst
- **Retry with Backoff** -- Exponential backoff with jitter for transient origin failures

### Infrastructure
- **Geographic Routing** -- Latency-based routing to nearest healthy edge region
- **Health Checking** -- Continuous HTTP health probes with configurable fail/success thresholds
- **Automatic Failover** -- Unhealthy nodes removed from routing; re-added on recovery
- **Prometheus Metrics** -- Full observability with `edgecdn_*` metric families
- **Grafana Dashboards** -- Pre-built dashboard for cache hit ratios, latency, and throughput

## Quick Start

### Prerequisites
- Go 1.21+
- Redis (optional -- falls back to in-memory LRU)
- Docker & Docker Compose (for full stack)

### Run Locally

```bash
# Build
go build -o edge-cdn ./cmd/router/

# Run as edge node
NODE_NAME=edge-01 NODE_REGION=us-east PORT=8080 ./edge-cdn

# Run as gateway
IS_GATEWAY=true EDGE_NODES="us-east=localhost:8081,eu-west=localhost:8082" PORT=8080 ./edge-cdn
```

### Docker Compose (Full Stack)

```bash
docker-compose up -d
```

This starts:
- 1 Gateway (port 8080)
- 3 Edge Nodes (us-east, eu-west, ap-south)
- 3 Redis instances
- Prometheus (port 9090)
- Grafana (port 3001)

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/*` | GET | CDN content delivery (cache lookup -> origin fetch) |
| `/health` | GET | Node health status |
| `/health/nodes` | GET | Peer node health (edge mode) |
| `/stats` | GET | Node statistics (uptime, node info) |
| `/metrics` | GET | Prometheus metrics |
| `/purge?path=/foo` | POST | Purge cached content |

## Response Headers

| Header | Description |
|--------|-------------|
| `X-Cache` | `HIT`, `MISS`, or `ERROR` |
| `X-Cache-Node` | Consistent hash target node |
| `X-Node-Name` | Serving node name |
| `X-Node-Region` | Serving node region |
| `X-Request-ID` | Unique request identifier |
| `X-Response-Time` | Total response latency |
| `X-Shield` | `coalesced` when origin shield deduplicated the request |

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP listen port |
| `NODE_NAME` | `edge-01` | Node identifier |
| `NODE_REGION` | `us-east` | Geographic region |
| `IS_GATEWAY` | `false` | Run as gateway (true) or edge node (false) |
| `REDIS_URL` | `localhost:6379` | Redis connection address |
| `ORIGIN_URL` | `http://origin:9090` | Origin server URL |
| `DEFAULT_TTL_SECONDS` | `300` | Default cache TTL |
| `CACHE_NODES` | `node-1,node-2,node-3` | Hash ring node list |
| `VNODE_COUNT` | `150` | Virtual nodes per physical node |
| `LRU_FALLBACK_SIZE` | `10000` | In-memory LRU cache capacity |
| `RATE_LIMIT_RPS` | `100` | Requests per second limit |
| `RATE_LIMIT_BURST` | `200` | Rate limiter burst size |
| `CB_FAIL_THRESHOLD` | `5` | Circuit breaker open after N failures |
| `CB_OPEN_TIMEOUT_SEC` | `30` | Circuit breaker recovery timeout |
| `LOG_LEVEL` | `info` | Log level (debug/info/warn/error) |
| `LOG_FORMAT` | `text` | Log format (text/json) |

## Project Structure

```
edge-cdn/
├── cmd/router/          # Application entrypoint
│   └── main.go          # Server wiring (edge + gateway modes)
├── internal/
│   ├── cache/           # Two-tier cache (Redis + LRU)
│   ├── circuitbreaker/  # Three-state circuit breaker
│   ├── gateway/         # Geo-routing gateway/load balancer
│   ├── hashing/         # Consistent hash ring (xxhash)
│   ├── health/          # HTTP health checker with failover
│   ├── metrics/         # Prometheus metrics + stats API
│   ├── origin/          # Origin server client (simulated)
│   ├── ratelimit/       # Token bucket rate limiter
│   ├── region/          # Geographic region routing
│   ├── retry/           # Exponential backoff with jitter
│   ├── router/          # HTTP router + CDN request handler
│   ├── shield/          # Origin shield (request coalescing)
│   └── warming/         # Proactive cache warming
├── k8s/                 # Kubernetes deployment manifests
├── monitoring/          # Prometheus + Grafana configs
├── loadtest/            # k6 load testing scripts
├── Dockerfile           # Multi-stage production build
├── docker-compose.yml   # Full local development stack
├── Makefile             # Build, test, and deploy targets
├── go.mod
└── go.sum
```

## Kubernetes Deployment

```bash
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/redis.yaml
kubectl apply -f k8s/edge-deployment.yaml
kubectl apply -f k8s/gateway-deployment.yaml
```

Includes HorizontalPodAutoscaler, topology spread constraints, and pod anti-affinity for production readiness.

## Load Testing

```bash
# Basic throughput test
k6 run loadtest/basic.js

# Cache warming test
k6 run loadtest/cache_warmup.js

# Failover test
k6 run loadtest/failover.js

# Multi-region routing test
k6 run loadtest/regions.js
```

## Performance

- **Cache HIT latency**: ~34 microseconds (in-memory LRU)
- **Cache MISS latency**: ~120ms (origin fetch + cache store)
- **Throughput**: 100+ RPS per node with rate limiting
- **Cache hit improvement**: 3,500x faster on subsequent requests

## License

MIT
