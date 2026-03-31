# Build stage
# Build context must be repo root: docker build -f services/rate-limiter/Dockerfile .
# WORKDIR mirrors repo layout so ../../proto/ratelimit.proto resolves correctly from build.rs
FROM rust:1.85-alpine AS builder
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG NO_PROXY
ENV HTTP_PROXY=$HTTP_PROXY
ENV HTTPS_PROXY=$HTTPS_PROXY
ENV NO_PROXY=$NO_PROXY
RUN apk add --no-cache musl-dev
WORKDIR /repo

# Proto files must be in place before any cargo build (build.rs compiles them)
COPY proto/ proto/

# Cache dependencies — dummy main.rs triggers a full dep compile with no source changes
COPY services/rate-limiter/Cargo.toml services/rate-limiter/Cargo.lock services/rate-limiter/
RUN mkdir -p services/rate-limiter/src \
    && echo 'fn main(){}' > services/rate-limiter/src/main.rs \
    && cargo build --release --manifest-path services/rate-limiter/Cargo.toml \
    && rm -rf services/rate-limiter/src

# Copy real source and rebuild
COPY services/rate-limiter/src services/rate-limiter/src/
COPY services/rate-limiter/build.rs services/rate-limiter/build.rs
RUN touch services/rate-limiter/src/main.rs \
    && cargo build --release --manifest-path services/rate-limiter/Cargo.toml

# Production stage
FROM alpine:3.19
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
WORKDIR /app
COPY --from=builder /repo/services/rate-limiter/target/release/rate-limiter .
EXPOSE 50051
HEALTHCHECK CMD nc -z localhost 50051 || exit 1
CMD ["./rate-limiter"]
