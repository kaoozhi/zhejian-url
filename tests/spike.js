// tests/spike.js
//
// Spike test: 100 → 1000 → 100 VUs over ~5 minutes.
// Validates the system does not error under sudden load increase.
// Error rate must stay below 1% throughout — latency may increase during spike.
// Tuned for WSL2 with ~16 GB RAM: safe ceiling ~1000–1200 VUs.
//
// Run:
//   K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/spike-report.html \
//   k6 run tests/spike.js
//
// Traffic flows: k6 → nginx(:80) → read-service (redirects) / write-service (setup POSTs)

import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost';

// Zipf-like weights: top URL ~40% of traffic, second ~20%, rest share remainder.
const WEIGHTS = [40, 20, 8, 7, 6, 5, 5, 5, 2, 2];

export const options = {
    stages: [
        { duration: '1m',  target: 100  }, // warm up
        { duration: '30s', target: 1000 }, // spike
        { duration: '1m',  target: 1000 }, // hold at peak
        { duration: '30s', target: 100  }, // recover
        { duration: '1m',  target: 100  }, // cooldown
    ],
    thresholds: {
        // Error rate must not degrade during spike.
        // Latency is expected to increase — no p95 threshold.
        http_req_failed: ['rate<0.01'],
    },
    gracefulStop: '5s', // default is 30s
};

export function setup() {
    const codes = [];
    for (let i = 0; i < 10; i++) {
        const res = http.post(
            `${BASE_URL}/api/v1/shorten`,
            JSON.stringify({ url: `https://example.com/spike-${i}` }),
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
        headers:   { 'X-Forwarded-For': vuIP(__VU) },
    });
    check(res, { 'redirect 301': (r) => r.status === 301 });
    sleep(0.7); // stay within 100 req/min per-IP rate limit
}

function vuIP(vu) {
    return `10.0.${Math.floor(vu / 256)}.${vu % 256}`;
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
