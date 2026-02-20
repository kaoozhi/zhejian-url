use redis::{aio::ConnectionManager, Script};
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
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)?
            .as_millis() as u64;

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
