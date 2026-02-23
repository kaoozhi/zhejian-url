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

#[cfg(test)]
mod tests {
    use super::*;
    use testcontainers::runners::AsyncRunner;
    use testcontainers_modules::redis::Redis;

    async fn make_service(
        rate: u32,
        burst: u32,
    ) -> (RateLimitService, testcontainers::ContainerAsync<Redis>) {
        let node = Redis::default().start().await.unwrap();
        let port = node.get_host_port_ipv4(6379).await.unwrap();
        let client = redis::Client::open(format!("redis://127.0.0.1:{port}")).unwrap();
        let conn = ConnectionManager::new(client).await.unwrap();
        (RateLimitService::new(conn, rate, burst), node)
    }

    #[tokio::test]
    async fn test_allowed_response_fields() {
        let (svc, _node) = make_service(10, 5).await;
        let req = Request::new(RateLimitRequest {
            ip: "1.1.1.1".into(),
        });

        let resp = svc.check_rate_limit(req).await.unwrap().into_inner();

        assert!(resp.allowed);
        assert_eq!(resp.remaining, 4); // burst=5, consumed 1
        assert_eq!(resp.retry_after_ms, 0);
    }

    #[tokio::test]
    async fn test_denied_response_fields() {
        let (svc, _node) = make_service(10, 3).await;

        // Exhaust the bucket
        for _ in 0..3 {
            let req = Request::new(RateLimitRequest {
                ip: "2.2.2.2".into(),
            });
            svc.check_rate_limit(req).await.unwrap();
        }

        let req = Request::new(RateLimitRequest {
            ip: "2.2.2.2".into(),
        });
        let resp = svc.check_rate_limit(req).await.unwrap().into_inner();

        assert!(!resp.allowed);
        assert_eq!(resp.remaining, 0);
        assert!(resp.retry_after_ms > 0);
    }

    #[tokio::test]
    async fn test_ip_is_extracted_from_request() {
        let (svc, _node) = make_service(10, 5).await;

        // Two different IPs must have independent buckets
        let r1 = svc
            .check_rate_limit(Request::new(RateLimitRequest {
                ip: "3.3.3.3".into(),
            }))
            .await
            .unwrap()
            .into_inner();
        let r2 = svc
            .check_rate_limit(Request::new(RateLimitRequest {
                ip: "4.4.4.4".into(),
            }))
            .await
            .unwrap()
            .into_inner();

        // Both fresh — both allowed with full remaining
        assert!(r1.allowed);
        assert!(r2.allowed);
        assert_eq!(r1.remaining, r2.remaining);
    }
}
