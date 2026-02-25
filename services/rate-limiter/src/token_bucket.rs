use redis::{Script, aio::ConnectionManager};
use std::time::{SystemTime, UNIX_EPOCH};

pub struct BucketResult {
    pub allowed: bool,
    pub remaining: i32,
    pub retry_after_ms: i64,
}

pub struct TokenBucket {
    script: Script,
    rate: u32,
    burst: u32,
}

impl TokenBucket {
    pub fn new(rate: u32, burst: u32) -> Self {
        Self {
            script: Script::new(include_str!("token_bucket.lua")),
            rate,
            burst,
        }
    }

    pub async fn check(
        &self,
        conn: &mut ConnectionManager,
        ip: &str,
    ) -> anyhow::Result<BucketResult> {
        let key = format!("rl:{ip}");
        let now = SystemTime::now().duration_since(UNIX_EPOCH)?.as_millis() as u64;

        let result: (i64, i64, i64) = self
            .script
            .key(&key)
            .arg(self.rate)
            .arg(self.burst)
            .arg(now)
            .invoke_async(conn)
            .await?;

        Ok(BucketResult {
            allowed: result.0 == 1,
            remaining: result.1 as i32,
            retry_after_ms: result.2,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use testcontainers::runners::AsyncRunner;
    use testcontainers_modules::redis::Redis;

    // Spins up a throwaway Redis container and returns a ConnectionManager
    // pointing at it. The container is dropped (stopped) when `_node` is dropped.
    async fn make_conn() -> (ConnectionManager, testcontainers::ContainerAsync<Redis>) {
        let node = Redis::default().start().await.unwrap();
        let port = node.get_host_port_ipv4(6379).await.unwrap();
        let url = format!("redis://127.0.0.1:{port}");
        let client = redis::Client::open(url).unwrap();
        let conn = ConnectionManager::new(client).await.unwrap();
        (conn, node)
    }

    #[tokio::test]
    async fn test_first_request_is_allowed() {
        let (mut conn, _node) = make_conn().await;
        let bucket = TokenBucket::new(10, 5);

        let result = bucket.check(&mut conn, "1.1.1.1").await.unwrap();
        assert!(result.allowed);
        assert_eq!(result.remaining, 4); // burst=5, consumed 1
        assert_eq!(result.retry_after_ms, 0);
    }

    #[tokio::test]
    async fn test_bucket_exhaustion_returns_429() {
        let (mut conn, _node) = make_conn().await;
        let burst = 3u32;
        let bucket = TokenBucket::new(10, burst);

        // Exhaust all tokens
        for _ in 0..burst {
            let r = bucket.check(&mut conn, "2.2.2.2").await.unwrap();
            assert!(r.allowed);
        }

        // Next request must be denied
        let result = bucket.check(&mut conn, "2.2.2.2").await.unwrap();
        assert!(!result.allowed);
        assert_eq!(result.remaining, 0);
        assert!(result.retry_after_ms > 0);
    }

    #[tokio::test]
    async fn test_remaining_decrements() {
        let (mut conn, _node) = make_conn().await;
        let bucket = TokenBucket::new(10, 5);

        let r1 = bucket.check(&mut conn, "3.3.3.3").await.unwrap();
        let r2 = bucket.check(&mut conn, "3.3.3.3").await.unwrap();

        assert!(r1.remaining > r2.remaining);
    }
}
