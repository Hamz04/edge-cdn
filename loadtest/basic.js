import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const cacheHitRate = new Rate('cache_hit_rate');
const responseTime = new Trend('response_time_ms');

export const options = {
  stages: [
    { duration: '30s', target: 50 },
    { duration: '1m', target: 100 },
    { duration: '30s', target: 200 },
    { duration: '30s', target: 0 },
  ],
  thresholds: {
    http_req_duration: ['p(95)<500', 'p(99)<1000'],
    http_req_failed: ['rate<0.01'],
    cache_hit_rate: ['rate>0.5'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const paths = [
  '/page/home', '/page/about', '/page/contact', '/page/products', '/page/pricing',
  '/api/data', '/api/users', '/api/orders', '/api/inventory',
  '/static/style.css', '/static/app.js', '/static/logo.png',
];

export default function () {
  const path = paths[Math.floor(Math.random() * paths.length)];
  const res = http.get(`${BASE_URL}${path}`);

  check(res, {
    'status is 200': (r) => r.status === 200,
    'has X-Cache header': (r) => r.headers['X-Cache'] !== undefined,
    'has X-Request-ID': (r) => r.headers['X-Request-ID'] !== undefined,
    'response body not empty': (r) => r.body && r.body.length > 0,
  });

  const isHit = res.headers['X-Cache'] === 'HIT';
  cacheHitRate.add(isHit);
  responseTime.add(res.timings.duration);

  sleep(0.05 + Math.random() * 0.1);
}

export function handleSummary(data) {
  const med = data.metrics.http_req_duration.values.med;
  const p95 = data.metrics.http_req_duration.values['p(95)'];
  const p99 = data.metrics.http_req_duration.values['p(99)'];
  const rps = data.metrics.http_reqs.values.rate;
  const hitRate = data.metrics.cache_hit_rate ? data.metrics.cache_hit_rate.values.rate : 0;

  console.log('\n=== EDGE CDN BENCHMARK RESULTS ===');
  console.log(`Requests/sec:  ${rps.toFixed(1)}`);
  console.log(`Median:        ${med.toFixed(2)}ms`);
  console.log(`P95:           ${p95.toFixed(2)}ms`);
  console.log(`P99:           ${p99.toFixed(2)}ms`);
  console.log(`Cache Hit Rate: ${(hitRate * 100).toFixed(1)}%`);
  console.log('==================================\n');

  return {
    'stdout': JSON.stringify(data, null, 2),
  };
}
