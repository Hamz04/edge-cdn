import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate } from 'k6/metrics';

const coldLatency = new Trend('cold_cache_latency');
const warmLatency = new Trend('warm_cache_latency');
const warmHitRate = new Rate('warm_cache_hit_rate');

export const options = {
  scenarios: {
    cold_cache: {
      executor: 'shared-iterations',
      vus: 10,
      iterations: 200,
      maxDuration: '1m',
      exec: 'coldPhase',
      startTime: '0s',
    },
    warm_cache: {
      executor: 'shared-iterations',
      vus: 10,
      iterations: 200,
      maxDuration: '1m',
      exec: 'warmPhase',
      startTime: '1m30s',
    },
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

const uniquePaths = [];
for (let i = 0; i < 200; i++) {
  uniquePaths.push(`/page/bench-${i}`);
}

export function coldPhase() {
  const idx = __ITER % uniquePaths.length;
  const res = http.get(`${BASE_URL}${uniquePaths[idx]}`);

  check(res, { 'cold: status 200': (r) => r.status === 200 });
  coldLatency.add(res.timings.duration);
  sleep(0.05);
}

export function warmPhase() {
  const idx = __ITER % uniquePaths.length;
  const res = http.get(`${BASE_URL}${uniquePaths[idx]}`);

  check(res, { 'warm: status 200': (r) => r.status === 200 });
  warmLatency.add(res.timings.duration);

  const isHit = res.headers['X-Cache'] === 'HIT';
  warmHitRate.add(isHit);
  sleep(0.05);
}

export function handleSummary(data) {
  const coldMed = data.metrics.cold_cache_latency ? data.metrics.cold_cache_latency.values.med : 0;
  const coldP99 = data.metrics.cold_cache_latency ? data.metrics.cold_cache_latency.values['p(99)'] : 0;
  const warmMed = data.metrics.warm_cache_latency ? data.metrics.warm_cache_latency.values.med : 0;
  const warmP99 = data.metrics.warm_cache_latency ? data.metrics.warm_cache_latency.values['p(99)'] : 0;
  const hitRate = data.metrics.warm_cache_hit_rate ? data.metrics.warm_cache_hit_rate.values.rate : 0;
  const speedup = coldMed > 0 ? (coldMed / warmMed).toFixed(1) : 'N/A';

  console.log('\n=== CACHE WARMUP COMPARISON ===');
  console.log(`Cold Cache Median:  ${coldMed.toFixed(2)}ms`);
  console.log(`Cold Cache P99:     ${coldP99.toFixed(2)}ms`);
  console.log(`Warm Cache Median:  ${warmMed.toFixed(2)}ms`);
  console.log(`Warm Cache P99:     ${warmP99.toFixed(2)}ms`);
  console.log(`Cache Hit Rate:     ${(hitRate * 100).toFixed(1)}%`);
  console.log(`Speedup:            ${speedup}x faster with warm cache`);
  console.log('===============================\n');
  return { 'stdout': JSON.stringify(data, null, 2) };
}
