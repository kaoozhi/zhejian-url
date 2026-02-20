fn main() -> Result<(), Box<dyn std::error::Error>> {
    let fds = protox::compile(["../../proto/ratelimit.proto"], ["../../proto"])?;
    tonic_build::configure()
        .build_server(true)
        .build_client(false) // Rust is the server, Go is the client
        .compile_fds(fds)?;
    Ok(())
}
