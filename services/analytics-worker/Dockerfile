FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o analytics-worker ./cmd/worker

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /build/analytics-worker .
CMD ["./analytics-worker"]
