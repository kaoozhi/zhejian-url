package analytics

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	exchangeName = "analytics"
	routingKey   = "analytics.clicks"
)

// Publisher wraps an AMQP connection and channel to publish click events
// to the analytics exchange. It is safe to call Publish on a nil Publisher
// (feature disabled when AMQP_URL is empty).
type Publisher struct {
	amqpURL string
	conn    *amqp.Connection
	channel *amqp.Channel
	logger  *slog.Logger
	mu      sync.RWMutex
	closed  atomic.Bool
}

// NewPublisher dials RabbitMQ and declares the analytics exchange.
// Returns (nil, nil) when amqpURL is empty — the publisher is disabled
// and all Publish calls become no-ops.
func NewPublisher(amqpURL string, logger *slog.Logger) (*Publisher, error) {
	if amqpURL == "" {
		return nil, nil
	}

	conn, ch, err := dial(amqpURL)
	if err != nil {
		return nil, err
	}

	p := &Publisher{amqpURL: amqpURL, conn: conn, channel: ch, logger: logger}
	go p.watchAndReconnect()

	return p, nil
}

// NewDegradedPublisher creates a publisher with a nil channel for testing
// the degraded-state no-op behaviour.
func NewDegradedPublisher(logger *slog.Logger) *Publisher {
	return &Publisher{logger: logger}
}

// publisherHeartbeat is the AMQP heartbeat interval requested by the publisher.
// The negotiated value is min(client, server); RabbitMQ defaults to 60 s so
// this 2 s value wins.  With two missed beats the library considers the
// connection dead, giving ≤4 s detection even when a TCP RST from a stopped
// broker is not propagated immediately (e.g. on WSL2).
const publisherHeartbeat = 2 * time.Second

// dial creates a new AMQP connection, opens a channel, and declares the exchange.
func dial(amqpURL string) (*amqp.Connection, *amqp.Channel, error) {
	conn, err := amqp.DialConfig(amqpURL, amqp.Config{
		Heartbeat: publisherHeartbeat,
	})
	if err != nil {
		return nil, nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, nil, err
	}

	if err := ch.ExchangeDeclare(
		exchangeName,
		"topic",
		true,  // durable
		false, // auto-delete
		false, // internal
		false, // no-wait
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, nil, err
	}

	return conn, ch, nil
}

// Publish encodes the event as JSON and publishes it fire-and-forget.
// Errors are logged but never returned — RabbitMQ unavailability must
// never affect redirect response latency or status code.
func (p *Publisher) Publish(ctx context.Context, event ClickEvent) {
	if p == nil {
		return
	}

	p.mu.RLock()
	ch := p.channel
	p.mu.RUnlock()

	if ch == nil {
		p.logger.WarnContext(ctx, "analytics: publisher degraded, dropping event")
		return
	}

	body, err := json.Marshal(event)
	if err != nil {
		p.logger.WarnContext(ctx, "analytics: failed to marshal event", slog.String("error", err.Error()))
		return
	}

	if err := ch.PublishWithContext(ctx,
		exchangeName,
		routingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	); err != nil {
		p.logger.WarnContext(ctx, "analytics: failed to publish click event",
			slog.String("error", err.Error()))
	}
}

// Close releases the AMQP channel and connection and stops the reconnect goroutine.
func (p *Publisher) Close() {
	if p == nil {
		return
	}
	p.closed.Store(true)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.channel != nil {
		p.channel.Close()
		p.channel = nil
	}

	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
}

// watchAndReconnect monitors the connection and re-dials on unexpected close.
// Retries indefinitely with exponential backoff (1s → 2s → 4s → … → 30s cap).
// Returns only on clean shutdown (Close called).
func (p *Publisher) watchAndReconnect() {
	for {
		p.mu.RLock()
		conn := p.conn
		p.mu.RUnlock()
		if conn == nil {
			return
		}

		closeCh := conn.NotifyClose(make(chan *amqp.Error, 1))
		amqpErr := <-closeCh

		if p.closed.Load() {
			return // clean shutdown via Close()
		}

		reason := "broker closed connection"
		if amqpErr != nil {
			reason = amqpErr.Error()
		}
		p.logger.Warn("analytics: AMQP connection lost, reconnecting",
			slog.String("reason", reason))

		p.mu.Lock()
		p.channel = nil // mark degraded during reconnect
		p.mu.Unlock()

		// Retry indefinitely with exponential backoff until reconnected or closed.
		backoff := 1 * time.Second
		const maxBackoff = 30 * time.Second
		for {
			if p.closed.Load() {
				return
			}
			time.Sleep(backoff)
			newConn, newCh, err := dial(p.amqpURL)
			if err != nil {
				p.logger.Warn("analytics: reconnect attempt failed", slog.String("error", err.Error()))
				if backoff < maxBackoff {
					backoff *= 2
				}
				continue
			}
			p.mu.Lock()
			p.conn = newConn
			p.channel = newCh
			p.mu.Unlock()
			p.logger.Info("analytics: AMQP reconnected successfully")
			break // reconnected — continue outer loop to watch new connection
		}
	}
}

// IsConnected reports whether the publisher has an active AMQP channel.
func (p *Publisher) IsConnected() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.channel != nil
}
