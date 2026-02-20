mod service;
mod token_bucket;

use service::{ratelimit::rate_limiter_server::RateLimiterServer, RateLimitService};
use tonic::transport::Server;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    let redis_url = std::env::var("REDIS_URL").unwrap_or("redis://localhost:6379".into());
    let port = std::env::var("GRPC_PORT").unwrap_or("50051".into());
    let rate: u32 = std::env::var("RATE_LIMIT")
        .unwrap_or("100".into())
        .parse()?;
    let burst: u32 = std::env::var("BURST").unwrap_or("50".into()).parse()?;

    let addr = format!("0.0.0.0:{port}").parse()?;

    // Redis multiplexed connection manager — auto-reconnects, cheaply cloneable
    let redis_client = redis::Client::open(redis_url)?;
    let redis_conn = redis::aio::ConnectionManager::new(redis_client).await?;

    let svc = RateLimiterServer::new(RateLimitService::new(redis_conn, rate, burst));

    println!("Rate limiter listening on {addr}");

    Server::builder().add_service(svc).serve(addr).await?;

    Ok(())
}
