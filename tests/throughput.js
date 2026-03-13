// tests/throughput.js
//
// Throughput test: 200 VUs, no sleep — measures the raw redirect ceiling.
//
// REQUIRES RATE LIMITER DISABLED:
//   RATE_LIMITER_ADDR="" k6 run tests/throughput.js
//
// Or restart the gateway without the rate limiter:
//   docker compose stop gateway
//   RATE_LIMITER_ADDR="" docker compose up -d gateway
//   k6 run tests/throughput.js
//   docker compose up -d  # restore original config
//
// Run with dashboard:
//   K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/throughput-report.html \
//   RATE_LIMITER_ADDR="" k6 run tests/throughput.js

import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// Zipf-like weights: top URL ~40% of traffic, second ~20%, rest share remainder.
const WEIGHTS = [40, 20, 8, 7, 6, 5, 5, 5, 2, 2];

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
    const codes = [];
    for (let i = 0; i < 10; i++) {
        const res = http.post(
            `${BASE_URL}/api/v1/shorten`,
            JSON.stringify({ url: `https://example.com/throughput-${i}` }),
            { headers: { 'Content-Type': 'application/json' } }
        );
        check(res, { 'setup: shorten 201': (r) => r.status === 201 });
        codes.push(res.json('short_code'));
    }
    return { codes };
}

export default function (data) {
    const code = pickWeighted(data.codes, WEIGHTS);
    const res = http.get(`${BASE_URL}/${code}`, {
        redirects: 0,
        // No X-Forwarded-For: rate limiter must be disabled for this test.
    });
    check(res, { 'redirect 301': (r) => r.status === 301 });
    // No sleep — drive maximum request rate.
}

function pickWeighted(items, weights) {
    const total = weights.reduce((a, b) => a + b, 0);
    let r = Math.random() * total;
    for (let i = 0; i < items.length; i++) {
        r -= weights[i];
        if (r <= 0) return items[i];
    }
    return items[items.length - 1];
}
