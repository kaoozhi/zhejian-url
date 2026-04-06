// tests/baseline.js
//
// Baseline load test: 100 VUs, 9 min (2+5+2), 80% redirect / 20% create.
// Per-VU X-Forwarded-For keeps each VU within the 100 req/min rate limit.
//
// Local run with dashboard:
//   K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/baseline-report.html \
//   k6 run tests/baseline.js
//
// With Prometheus remote write (requires --web.enable-remote-write-receiver on Prometheus):
//   K6_PROMETHEUS_RW_SERVER_URL=http://localhost:9090/api/v1/write \
//   k6 run -o experimental-prometheus-rw tests/baseline.js
//
// CI run (shorter 1+2+1 stages):
//   CI=true k6 run tests/baseline.js
//
// Traffic flows: k6 → nginx(:80) → read-service (redirects) / write-service (creates)

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost';

// Zipf-like weights: top URL ~40% of traffic, second ~20%, rest share remainder.
const WEIGHTS = [40, 20, 8, 7, 6, 5, 5, 5, 2, 2];

const redirectErrors = new Rate('redirect_errors');
const createErrors   = new Rate('create_errors');

const stages = __ENV.CI === 'true'
    ? [
        { duration: '1m', target: 100 },
        { duration: '2m', target: 100 },
        { duration: '1m', target: 0   },
      ]
    : [
        { duration: '2m', target: 100 },
        { duration: '5m', target: 100 },
        { duration: '2m', target: 0   },
      ];

export const options = {
    stages,
    thresholds: {
        // p95 < 200ms for WSL2/Docker. Tighten to 50ms on native Linux.
        'http_req_duration{type:redirect}': ['p(95)<200'],
        'http_req_duration{type:create}':   ['p(95)<500'],
        http_req_failed: ['rate<0.01'],
        redirect_errors: ['rate<0.01'],
        create_errors:   ['rate<0.01'],
    },
};

export function setup() {
    const codes = [];
    for (let i = 0; i < 10; i++) {
        const res = http.post(
            `${BASE_URL}/api/v1/shorten`,
            JSON.stringify({ url: `https://example.com/baseline-${i}` }),
            { headers: { 'Content-Type': 'application/json' } }
        );
        check(res, { 'setup: shorten 201': (r) => r.status === 201 });
        codes.push(res.json('short_code'));
    }
    console.log(`Baseline: created ${codes.length} short URLs`);
    return { codes };
}

export default function (data) {
    const ip = vuIP(__VU);

    if (Math.random() < 0.8) {
        // 80%: redirect
        const code = pickWeighted(data.codes, WEIGHTS);
        const res = http.get(`${BASE_URL}/${code}`, {
            redirects: 0,
            headers:   { 'X-Forwarded-For': ip },
            tags:      { type: 'redirect' },
        });
        const ok = check(res, { 'redirect 301': (r) => r.status === 301 });
        redirectErrors.add(!ok);
        sleep(0.7); // 1.4 req/s — within 100 req/min per-IP limit
    } else {
        // 20%: create short URL
        const res = http.post(
            `${BASE_URL}/api/v1/shorten`,
            JSON.stringify({ url: `https://example.com/load-${Date.now()}-${__VU}` }),
            {
                headers: { 'Content-Type': 'application/json', 'X-Forwarded-For': ip },
                tags:    { type: 'create' },
            }
        );
        const ok = check(res, { 'create 201': (r) => r.status === 201 });
        createErrors.add(!ok);
        sleep(1.0); // creates are heavier; slower cadence
    }
}

// vuIP maps a VU number to a unique spoofed IP so each VU has its own
// rate-limit token bucket (simulates distinct users).
function vuIP(vu) {
    return `10.0.${Math.floor(vu / 256)}.${vu % 256}`;
}

// pickWeighted selects an item using a weighted random distribution.
function pickWeighted(items, weights) {
    const total = weights.reduce((a, b) => a + b, 0);
    let r = Math.random() * total;
    for (let i = 0; i < items.length; i++) {
        r -= weights[i];
        if (r <= 0) return items[i];
    }
    return items[items.length - 1];
}
