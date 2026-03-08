import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Counter } from 'k6/metrics';

const failoverRate = new Rate('failover_rate');
const failoverCount = new Counter('failover_count');

export const options = {
  stages: [
    { duration: '30s', target: 50 },
    { duration: '2m', target: 50 },
    { duration: '20s', target: 0 },
  ],
  thresholds: {
    http_req_failed: ['rate<0.05'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const paths = ['/page/home', '/page/about', '/api/data', '/api/users', '/static/style.css'];

export default function () {
  const path = paths[Math.floor(Math.random() * paths.length)];
  const res = http.get(`${BASE_URL}${path}`);

  const isFailover = res.headers['X-Failover'] === 'true';
  failoverRate.add(isFailover);
  if (isFailover) {
    failoverCount.add(1);
  }

  check(res, {
    'status is 200 (even during failover)': (r) => r.status === 200,
    'response not empty': (r) => r.body && r.body.length > 0,
  });

  if (isFailover) {
    console.log(`FAILOVER detected: ${res.headers['X-Primary-Node'] || 'unknown'} -> ${res.headers['X-Serving-Node'] || res.headers['X-Gateway-Region'] || 'unknown'}`);
  }

  sleep(0.1);
}

export function handleSummary(data) {
  const total = data.metrics.http_reqs.values.count;
  const failovers = data.metrics.failover_count ? data.metrics.failover_count.values.count : 0;
  const successRate = data.metrics.http_req_failed ? (1 - data.metrics.http_req_failed.values.rate) * 100 : 100;

  console.log('\n=== FAILOVER TEST RESULTS ===');
  console.log(`Total requests:  ${total}`);
  console.log(`Failover events: ${failovers}`);
  console.log(`Success rate:    ${successRate.toFixed(2)}%`);
  console.log(`Instructions: During this test, kill an edge node with:`);
  console.log(`  docker stop cdn-edge-us-east`);
  console.log(`Then watch failover events increase.`);
  console.log(`Restart with: docker start cdn-edge-us-east`);
  console.log('=============================\n');
  return { 'stdout': JSON.stringify(data, null, 2) };
}
