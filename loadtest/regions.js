import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

const regionHits = {
  'us-east': new Counter('region_us_east'),
  'eu-west': new Counter('region_eu_west'),
  'ap-south': new Counter('region_ap_south'),
};

export const options = {
  stages: [
    { duration: '20s', target: 30 },
    { duration: '1m', target: 60 },
    { duration: '20s', target: 0 },
  ],
  thresholds: {
    http_req_failed: ['rate<0.01'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const regions = ['us-east', 'eu-west', 'ap-south'];
const paths = ['/page/home', '/api/data', '/static/style.css'];

export default function () {
  const region = regions[Math.floor(Math.random() * regions.length)];
  const path = paths[Math.floor(Math.random() * paths.length)];

  const res = http.get(`${BASE_URL}${path}`, {
    headers: { 'X-Region': region },
  });

  check(res, {
    'status is 200': (r) => r.status === 200,
    'has gateway header': (r) => r.headers['X-Gateway'] !== undefined,
    'routed to correct region': (r) => {
      const nodeRegion = r.headers['X-Node-Region'] || r.headers['X-Gateway-Region'] || '';
      return nodeRegion.includes(region);
    },
  });

  if (regionHits[region]) {
    regionHits[region].add(1);
  }

  sleep(0.1);
}

export function handleSummary(data) {
  console.log('\n=== REGION ROUTING RESULTS ===');
  for (const region of regions) {
    const key = `region_${region.replace('-', '_')}`;
    const count = data.metrics[key] ? data.metrics[key].values.count : 0;
    console.log(`${region}: ${count} requests`);
  }
  console.log('==============================\n');
  return { 'stdout': JSON.stringify(data, null, 2) };
}
