pub mod ratelimit {
    tonic::include_proto!("ratelimit");
}

use crate::token_bucket::TokenBucket;
use ratelimit::rate_limiter_server::RateLimiter;
use ratelimit::{RateLimitRequest, RateLimitResponse};
use redis::aio::ConnectionManager;
use tonic::{Request, Response, Status};

pub struct RateLimitService {
    redis: ConnectionManager,
    bucket: TokenBucket,
}

impl RateLimitService {
    pub fn new(redis: ConnectionManager, rate: u32, burst: u32) -> Self {
        Self {
            redis,
            bucket: TokenBucket::new(rate, burst),
        }
    }
}

#[tonic::async_trait]
impl RateLimiter for RateLimitService {
    async fn check_rate_limit(
        &self,
        request: Request<RateLimitRequest>,
    ) -> Result<Response<RateLimitResponse>, Status> {
        let ip = request.into_inner().ip;
        let mut conn = self.redis.clone();

        let result = self
            .bucket
            .check(&mut conn, &ip)
            .await
            .map_err(|e| Status::internal(e.to_string()))?;

        Ok(Response::new(RateLimitResponse {
            allowed: result.allowed,
            remaining: result.remaining,
            retry_after_ms: result.retry_after_ms,
        }))
    }
}
