// tests/throughput.js
//
// Throughput test: 200 VUs, no sleep — measures the raw redirect ceiling.
//
// REQUIRES RATE LIMITER DISABLED:
//   RATE_LIMITER_ADDR="" docker compose up -d read-service
//
// Run with dashboard:
//   K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/throughput-report.html \
//   k6 run tests/throughput.js
//
// Traffic flows (docker):  k6 → nginx(:80) → read-service (redirects) / write-service (POSTs)
// Traffic flows (host):    k6 → read-server(:8080) redirects, write-server(:8081) POSTs
//   BASE_URL=http://localhost:8080 WRITE_URL=http://localhost:8081 k6 run tests/throughput.js

import http from 'k6/http';
import { check } from 'k6';

const BASE_URL  = __ENV.BASE_URL  || 'http://localhost';
const WRITE_URL = __ENV.WRITE_URL || BASE_URL;

// 300 URLs: large enough that SHA-256 consistent hashing spreads keys evenly
// across all ring nodes (±5% per node expected with 150 vnodes).
const URL_COUNT = 300;

export const options = {
    stages: [
        { duration: '30s', target: 700 },
        { duration: '1m30s',  target: 700 },
        { duration: '30s', target: 0   },
    ],
    thresholds: {
        // No latency threshold — this test measures throughput ceiling, not SLO compliance.
        http_req_failed: ['rate<0.01'],
    },
};

export function setup() {
    const runId = Date.now();
    const codes = [];
    for (let i = 0; i < URL_COUNT; i++) {
        const res = http.post(
            `${WRITE_URL}/api/v1/shorten`,
            JSON.stringify({ url: `https://example.com/throughput-${runId}-${i}` }),
            { headers: { 'Content-Type': 'application/json' } }
        );
        check(res, { 'setup: shorten 201': (r) => r.status === 201 });
        const code = res.json('short_code');
        if (code) codes.push(code);
    }
    return { codes };
}

export default function (data) {
    // Uniform random selection: each key gets equal traffic,
    // so node load tracks the ring's key distribution (~33% each with 3 nodes).
    const code = data.codes[Math.floor(Math.random() * data.codes.length)];
    const res = http.get(`${BASE_URL}/${code}`, {
        redirects: 0,
        // No X-Forwarded-For: rate limiter must be disabled for this test (read-service).
    });
    check(res, { 'redirect 301': (r) => r.status === 301 });
    // No sleep — drive maximum request rate.
}
