// tests/hotkey.js
//
// Hot-key stress test: small URL pool (20 URLs) so keys concentrate on one or two
// ring nodes by hash assignment. Used to demonstrate per-node circuit breaker
// behaviour in Phase 11: with a single CB the entire ring trips; with per-node
// CBs only the saturated node's CB opens, leaving the other nodes serving
// cache hits normally.
//
// REQUIRES RATE LIMITER DISABLED:
//   RATE_LIMITER_ADDR="" docker compose up -d gateway
//   curl -s http://localhost:8080/health | jq 'has("rate_limiter_cb")'
//   # Expected: false
//
// Run before/after per-node CB:
//   K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT=results/hotkey-single-cb.html \
//   script -q -c "k6 run tests/hotkey.js" results/hotkey-single-cb.log
//
// Watch CB behaviour in a second terminal while the test runs:
//   watch -n1 'docker compose logs --tail=5 gateway | grep -i "circuit\|too many"'
//
// Key metrics to compare:
//   grep -E "p\(95\)|http_reqs\b|http_req_failed" results/hotkey-single-cb.log

import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

// 20 URLs: small enough that SHA-256 hashing concentrates most keys on one or
// two ring nodes, reproducing the hot-key saturation pattern.
const URL_COUNT = 20;

export const options = {
    stages: [
        { duration: '30s', target: 700 },
        { duration: '1m30s', target: 700 },
        { duration: '30s', target: 0 },
    ],
    thresholds: {
        // No latency threshold — this test measures CB behaviour under hot-key load.
        http_req_failed: ['rate<0.01'],
    },
};

export function setup() {
    const runId = Date.now();
    const codes = [];
    for (let i = 0; i < URL_COUNT; i++) {
        const res = http.post(
            `${BASE_URL}/api/v1/shorten`,
            JSON.stringify({ url: `https://example.com/hotkey-${runId}-${i}` }),
            { headers: { 'Content-Type': 'application/json' } }
        );
        check(res, { 'setup: shorten 201': (r) => r.status === 201 });
        const code = res.json('short_code');
        if (code) codes.push(code);
    }
    return { codes };
}

export default function (data) {
    // Uniform random selection over a small pool: each key gets equal traffic,
    // so whichever node owns the majority of the 20 keys gets saturated.
    const code = data.codes[Math.floor(Math.random() * data.codes.length)];
    const res = http.get(`${BASE_URL}/${code}`, {
        redirects: 0,
    });
    check(res, { 'redirect 301': (r) => r.status === 301 });
    // No sleep — maximise pressure on the hot node.
}
