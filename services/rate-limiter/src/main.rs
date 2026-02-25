mod service;
mod token_bucket;

use service::{RateLimitService, ratelimit::rate_limiter_server::RateLimiterServer};
use tonic::transport::Server;
pub struct RateLimitConfig {
    redis_url: String,
    port: String,
    rate: u32,
    burst: u32,
}

impl RateLimitConfig {
    pub fn load() -> anyhow::Result<Self> {
        Ok(Self {
            redis_url: std::env::var("REDIS_URL").unwrap_or("redis://localhost:6379".into()),
            port: std::env::var("GRPC_PORT").unwrap_or("50051".into()),
            rate: std::env::var("RATE_LIMIT")
                .unwrap_or("100".into())
                .parse()?,
            burst: std::env::var("BURST").unwrap_or("50".into()).parse()?,
        })
    }
}

impl Default for RateLimitConfig {
    fn default() -> Self {
        Self {
            redis_url: "redis://localhost:6379".to_string(),
            port: "50051".to_string(),
            rate: 100,
            burst: 50,
        }
    }
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let cfg = RateLimitConfig::load().unwrap_or_default();
    let addr = format!("0.0.0.0:{}", cfg.port).parse()?;

    // Redis multiplexed connection manager — auto-reconnects, cheaply cloneable
    let redis_client = redis::Client::open(cfg.redis_url)?;
    let redis_conn = redis::aio::ConnectionManager::new(redis_client).await?;
    let svc = RateLimiterServer::new(RateLimitService::new(redis_conn, cfg.rate, cfg.burst));
    println!("Rate limiter listening on {addr}");

    Server::builder().add_service(svc).serve(addr).await?;

    Ok(())
}
