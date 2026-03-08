.PHONY: build run test clean docker-build docker-up docker-down docker-logs test-basic test-regions test-failover test-cache bench

# --- Build ---
build:
	go build -ldflags="-s -w" -o bin/edge-cdn ./cmd/router

run: build
	./bin/edge-cdn

test:
	go test -v -race ./...

clean:
	rm -rf bin/
	go clean

vet:
	go vet ./...

# --- Docker ---
docker-build:
	docker-compose build

docker-up:
	docker-compose up -d
	@echo "Gateway:    http://localhost:8080"
	@echo "Prometheus: http://localhost:9090"
	@echo "Grafana:    http://localhost:3000 (admin/edgecdn)"
	@echo ""
	@echo "Waiting for services to start..."
	@sleep 5
	@echo "Ready! Run 'make test-basic' to start load testing."

docker-down:
	docker-compose down -v

docker-logs:
	docker-compose logs -f

docker-restart:
	docker-compose down -v && docker-compose up -d

# --- Load Tests (requires k6: https://k6.io/docs/getting-started/installation/) ---
test-basic:
	k6 run loadtest/basic.js

test-regions:
	k6 run loadtest/regions.js

test-failover:
	@echo "Starting failover test. During the test, run in another terminal:"
	@echo "  docker stop cdn-edge-us-east"
	@echo "Then restart with:"
	@echo "  docker start cdn-edge-us-east"
	@echo ""
	k6 run loadtest/failover.js

test-cache:
	k6 run loadtest/cache_warmup.js

# --- Full benchmark suite ---
bench: docker-up
	@echo "=== Running full benchmark suite ==="
	@echo ""
	@echo "--- Basic Load Test ---"
	k6 run --quiet loadtest/basic.js
	@echo ""
	@echo "--- Region Routing Test ---"
	k6 run --quiet loadtest/regions.js
	@echo ""
	@echo "--- Cache Warmup Test ---"
	k6 run --quiet loadtest/cache_warmup.js
	@echo ""
	@echo "=== Benchmark suite complete ==="
	@echo "View Grafana dashboard: http://localhost:3000"
