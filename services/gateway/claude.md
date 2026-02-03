# zhejian-url shortener

A production-grade URL shortener service built as a learning and portfolio project.

## Tech Stack

- **Language**: Go 1.21+
- **Framework**: Gin (HTTP router)
- **Database**: PostgreSQL (pgx driver)
- **Cache**: Redis (go-redis)
- **Testing**: Testcontainers, testify

## Project Structure

```
services/gateway/
├── cmd/server/          # Application entrypoint
├── integration_test/    # Integration tests (real DB/cache)
└── internal/
    ├── api/             # HTTP handlers
    ├── config/          # Configuration loading
    ├── infra/           # Infrastructure (DB/cache clients)
    ├── model/           # Domain models and DTOs
    ├── repository/      # Data access layer
    ├── server/          # HTTP server setup
    ├── service/         # Business logic
    └── testutil/        # Test helpers
```

## Architecture

Clean/layered architecture with dependency injection:

```
Handler (API) → Service (Business Logic) → Repository (Data Access)
```

- Each layer has its own package under `internal/`
- Dependencies flow inward (handler depends on service, service depends on repository)
- Use interfaces for testability with compile-time checks

## Coding Conventions

### General

- Use `context.Context` as first parameter for all repository/service methods
- Prefer interfaces for testability with compile-time checks: `var _ Interface = (*Impl)(nil)`
- Constructors: `NewXxx(deps) *Xxx`
- Interfaces: `XxxInterface` (e.g., `URLServiceInterface`)

### Error Handling

- Define package-level sentinel errors: `var ErrNotFound = errors.New("...")`
- Use `errors.Is()` for error comparison
- Map lower-layer errors to domain errors (repo → service → handler)

### Testing

- Use testify for assertions (`assert`, `require`)
- Use `t.Run()` for subtests
- Use `TestMain` for shared fixtures (DB, cache containers)
- Use testcontainers for integration tests (real Postgres, Redis)
- Place test helpers in `internal/testutil/`

### Configuration

- Load from environment variables with `.env` support (godotenv)
- Provide sensible defaults via `getEnv()` helpers
- Group related config in structs: `ServerConfig`, `DatabaseConfig`, `CacheConfig`, `AppConfig`

### API Responses

- JSON with `snake_case` field names
- Use typed request/response structs from `model` package
- Errors use `ErrorResponse{Error, Message}`

## Do NOT Guidelines

### Architecture

- **Don't bypass layers** - Never call repository directly from handler; always go through service
- **Don't use global state for dependencies** - Use dependency injection via constructors, not package-level variables
- **Don't put business logic in handlers** - Handlers should only handle HTTP concerns (parse request, call service, format response)

### Error Handling

- **Don't use `panic` for recoverable errors** - Return errors and handle them properly
- **Don't ignore errors** - Always handle or explicitly ignore with `_ =` and a comment explaining why
- **Don't expose internal errors to API clients** - Map to appropriate HTTP status codes and user-friendly messages

### Configuration

- **Don't hardcode configuration values** - Use the `config` package and environment variables
- **Don't commit `.env` files** - Use `.env.example` for documentation

### Testing

- **Don't use mocks when testcontainers work** - Prefer real dependencies for integration tests
- **Don't skip tests for new functionality** - All new code should have corresponding tests
- **Don't use `t.Errorf` alone when test should stop** - Use `require` for fatal assertions, `assert` for non-fatal

### Code Style

- **Don't use `fmt.Println` for logging** - Use `log` package (will migrate to structured logging)
- **Don't use `interface{}` or `any` without good reason** - Prefer typed parameters
- **Don't use `init()` functions** - Prefer explicit initialization in `main()` or constructors

### Dependencies

- **Don't add new dependencies without consideration** - Discuss new packages before adding to go.mod
- **Don't use ORM** - Use raw SQL with pgx for full control and performance

## Logging (Planned)

Currently using standard `log` package. Will migrate to:

- Structured logging with slog or zerolog
- Distributed tracing with OpenTelemetry
- Correlation IDs for request tracing

Until then: use `log` package, avoid `fmt.Print` for logging.

## Commands

```bash
# Run tests
go test ./...

# Run integration tests
go test ./integration_test/

# Run server
go run cmd/server/main.go

# Run with docker
docker-compose up

# Run linter
golangci-lint run
```
