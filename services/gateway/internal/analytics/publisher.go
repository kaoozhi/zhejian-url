package analytics

import (
	"context"
	"encoding/json"
	"log/slog"

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
	conn    *amqp.Connection
	channel *amqp.Channel
	logger  *slog.Logger
}

// NewPublisher dials RabbitMQ and declares the analytics exchange.
// Returns (nil, nil) when amqpURL is empty — the publisher is disabled
// and all Publish calls become no-ops.
func NewPublisher(amqpURL string, logger *slog.Logger) (*Publisher, error) {
	if amqpURL == "" {
		return nil, nil
	}

	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, err
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
		return nil, err
	}

	return &Publisher{conn: conn, channel: ch, logger: logger}, nil
}

// Publish encodes the event as JSON and publishes it fire-and-forget.
// Errors are logged but never returned — RabbitMQ unavailability must
// never affect redirect response latency or status code.
func (p *Publisher) Publish(ctx context.Context, event ClickEvent) {
	if p == nil {
		return
	}
	body, err := json.Marshal(event)
	if err != nil {
		p.logger.WarnContext(ctx, "analytics: failed to marshal event", slog.String("error", err.Error()))
		return
	}

	if err := p.channel.PublishWithContext(ctx,
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

// Close releases the AMQP channel and connection. Called on graceful shutdown.
func (p *Publisher) Close() {
	if p == nil {
		return
	}
	if p.channel != nil {
		p.channel.Close()
	}

	if p.conn != nil {
		p.conn.Close()
	}
}
