FROM alpine:3.19

# Install migrate CLI (v4.19.1 to match go.mod)
RUN apk add --no-cache ca-certificates curl postgresql-client && \
    curl -L https://github.com/golang-migrate/migrate/releases/download/v4.19.1/migrate.linux-amd64.tar.gz | tar xvz && \
    mv migrate /usr/local/bin/migrate


WORKDIR /migrations
COPY schema/*.sql ./
COPY migrate-entrypoint.sh /
RUN chmod +x /migrate-entrypoint.sh

ENTRYPOINT ["/migrate-entrypoint.sh"]