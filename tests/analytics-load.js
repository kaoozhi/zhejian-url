// tests/analytics-load.js
//
// Simulates realistic click traffic to demonstrate the async analytics pipeline.
//
// Usage:
//   k6 run tests/analytics-load.js
//   k6 run tests/analytics-load.js -e BASE_URL=http://localhost
//
// What it does:
//   setup()   — creates 10 short URLs
//   default() — 50 VUs hit redirects for 65s with Zipf-like distribution
//                → generates ~4500 click events
//
// Rate limiter note:
//   The gateway rate limiter allows 100 req/min (1.67 req/s) per IP with burst=50.
//   Each VU gets a distinct IP via X-Forwarded-For, and sleeps 700ms between
//   requests (~1.4 req/s), staying within the per-IP limit.
//
// After the run, verify the pipeline worked:
//   sleep 6
//   docker compose exec postgres psql -U zhejian -d urlshortener \
//     -c "SELECT short_code, count(*) FROM analytics GROUP BY short_code ORDER BY count DESC;"
//
// Expected: ~4500 rows total, top URL ~40% of rows.

// End-to-end flow per iteration:
// k6 VU                  Gateway              RabbitMQ           analytics-worker
//   │                        │                    │                      │
//   │─── GET /BTs3VO ───────>│                    │                      │
//   │   X-Forwarded-For:     │─ check rate limit─>│                      │
//   │   10.0.0.7             │<─ allowed ─────────│                      │
//   │                        │── 301 redirect ───>│                      │
//   │<── HTTP 301 ───────────│                    │                      │
//   │                        │── publish event ──>│                      │
//   │   sleep(0.7s)          │                    │── deliver ──────────>│
//   │                        │                    │             (batch accumulates)
//   │                        │                    │             flush every 5s or 100 events
//   │                        │                    │<─── Ack ─────────────│
//   │                        │                    │             INSERT INTO analytics


import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost';

// Zipf-like weights: top URL gets ~40% of traffic, second ~20%, rest share the remainder.
// This mirrors real-world link popularity distributions.
const WEIGHTS = [40, 20, 8, 7, 6, 5, 5, 5, 2, 2];

export const options = {
    stages: [
        { duration: '10s', target: 50 }, // ramp up to 50 VUs
        { duration: '50s', target: 50 }, // steady load
        { duration: '5s',  target: 0  }, // ramp down
    ],
    thresholds: {
        http_req_failed: ['rate<0.01'], // <1% error rate
    },
};

// setup() runs once before the load test, creates 10 short URLs and
// returns their codes to all VUs via the shared data object.
export function setup() {
    const codes = [];
    for (let i = 0; i < 10; i++) {
        const res = http.post(
            `${BASE_URL}/api/v1/shorten`,
            JSON.stringify({ url: `https://example.com/page-${i}` }),
            { headers: { 'Content-Type': 'application/json' } }
        );
        check(res, { 'shorten 201': (r) => r.status === 201 });
        codes.push(res.json('short_code'));
    }
    console.log(`Created ${codes.length} short URLs: ${codes.join(', ')}`);
    return { codes };
}

// default() is called once per VU per iteration.
// It picks a URL using weighted random selection and fires a redirect request.
// Each VU sends a distinct X-Forwarded-For header so the per-IP rate limiter
// treats every VU as a separate client — simulating traffic from real distinct users.
export default function (data) {
    const code = pickWeighted(data.codes, WEIGHTS);
    const res = http.get(`${BASE_URL}/${code}`, {
        redirects: 0, // don't follow the 301 — we just want to trigger the event
        headers: { 'X-Forwarded-For': `10.0.${Math.floor(__VU / 256)}.${__VU % 256}` },
    });
    check(res, { 'redirect 301': (r) => r.status === 301 });
    sleep(0.7); // 700ms think time → ~1.4 req/s per VU, within 100 req/min per-IP limit
}

// pickWeighted selects an item using a weighted random distribution.
// Higher weight = higher probability of being selected.
function pickWeighted(items, weights) {
    const total = weights.reduce((a, b) => a + b, 0);
    let r = Math.random() * total;
    for (let i = 0; i < items.length; i++) {
        r -= weights[i];
        if (r <= 0) return items[i];
    }
    return items[items.length - 1];
}
