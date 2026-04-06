// tests/endurance.js
//
// Endurance test: 100 VUs for 12 minutes (1+10+1).
// Detects memory leaks, connection pool exhaustion, AMQP reconnect instability.
//
// LOCAL ONLY — not run in CI.
//
// Run:
//   K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/endurance-report.html \
//   k6 run tests/endurance.js
//
// Traffic flows: k6 → nginx(:80) → read-service (redirects) / write-service (creates)

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost';

// Zipf-like weights: top URL ~40% of traffic, second ~20%, rest share remainder.
const WEIGHTS = [40, 20, 8, 7, 6, 5, 5, 5, 2, 2];

const redirectErrors = new Rate('redirect_errors');

export const options = {
    stages: [
        { duration: '1m',  target: 100 },
        { duration: '10m', target: 100 },
        { duration: '1m',  target: 0   },
    ],
    thresholds: {
        'http_req_duration{type:redirect}': ['p(95)<200'],
        http_req_failed: ['rate<0.01'],
        redirect_errors: ['rate<0.01'],
    },
};

export function setup() {
    const codes = [];
    for (let i = 0; i < 10; i++) {
        const res = http.post(
            `${BASE_URL}/api/v1/shorten`,
            JSON.stringify({ url: `https://example.com/endurance-${i}` }),
            { headers: { 'Content-Type': 'application/json' } }
        );
        check(res, { 'setup: shorten 201': (r) => r.status === 201 });
        codes.push(res.json('short_code'));
    }
    return { codes };
}

export default function (data) {
    const ip = vuIP(__VU);

    if (Math.random() < 0.8) {
        const code = pickWeighted(data.codes, WEIGHTS);
        const res = http.get(`${BASE_URL}/${code}`, {
            redirects: 0,
            headers:   { 'X-Forwarded-For': ip },
            tags:      { type: 'redirect' },
        });
        const ok = check(res, { 'redirect 301': (r) => r.status === 301 });
        redirectErrors.add(!ok);
        sleep(0.7);
    } else {
        const res = http.post(
            `${BASE_URL}/api/v1/shorten`,
            JSON.stringify({ url: `https://example.com/load-${Date.now()}-${__VU}` }),
            {
                headers: { 'Content-Type': 'application/json', 'X-Forwarded-For': ip },
                tags:    { type: 'create' },
            }
        );
        check(res, { 'create 201': (r) => r.status === 201 });
        sleep(1.0);
    }
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
