package consumer

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/zhejian/url-shortener/analytics-worker/internal/repository"
)

const (
	queueName    = "analytics.clicks"
	dlqName      = "analytics.clicks.dlq"
	exchangeName = "analytics"
	routingKey   = "analytics.clicks"
)

// Consumer reads click events from RabbitMQ and bulk-inserts them into PostgreSQL.
// It accumulates deliveries into a batch and flushes when either:
//   - the batch reaches batchSize, or
//   - flushInterval elapses (whichever comes first).
type Consumer struct {
	conn          *amqp.Connection
	repo          *repository.Repository
	logger        *slog.Logger
	batchSize     int
	flushInterval time.Duration
}

// New creates a Consumer. Call Setup() to declare the AMQP topology, then Run() to start consuming.
func New(conn *amqp.Connection, repo *repository.Repository, logger *slog.Logger, batchSize int, flushInterval time.Duration) *Consumer {
	return &Consumer{
		conn:          conn,
		repo:          repo,
		logger:        logger,
		batchSize:     batchSize,
		flushInterval: flushInterval,
	}
}

// Setup declares the exchange, DLQ, and main queue — idempotent, safe to call on every startup.
//
// Topology:
//
//	exchange "analytics" (topic, durable)
//	    └── queue "analytics.clicks" (durable, x-dead-letter → dlq)
//	    └── queue "analytics.clicks.dlq" (durable, catch-all for nack'd messages)
func (c *Consumer) Setup() error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	// Declare the topic exchange — gateway publishes here.
	if err := ch.ExchangeDeclare(exchangeName, "topic", true, false, false, false, nil); err != nil {
		return err
	}

	// Declare the DLQ first so it exists before the main queue references it.
	if _, err := ch.QueueDeclare(dlqName, true, false, false, false, nil); err != nil {
		return err
	}

	// Declare the main queue with dead-letter routing:
	// any nack(requeue=false) is forwarded to dlqName via the default exchange ("").
	args := amqp.Table{
		"x-dead-letter-exchange":    "",      // default exchange routes by queue name
		"x-dead-letter-routing-key": dlqName, // target queue for dead-lettered messages
	}
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, args); err != nil {
		return err
	}

	// Bind the main queue to the exchange on the gateway's routing key.
	return ch.QueueBind(queueName, routingKey, exchangeName, false, nil)
}

// Run starts the consume loop. Blocks until ctx is cancelled (graceful shutdown)
// or the AMQP channel closes unexpectedly.
//
// On shutdown, any buffered but un-flushed messages are flushed before returning.
func (c *Consumer) Run(ctx context.Context) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	// Prefetch 2× batchSize so RabbitMQ keeps the worker busy without
	// overwhelming it — at most one full batch is in-flight at any time.
	if err := ch.Qos(c.batchSize*2, 0, false); err != nil {
		return err
	}

	// autoAck=false: we ack only after a successful bulk insert.
	deliveries, err := ch.Consume(queueName, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	var batch []amqp.Delivery
	ticker := time.NewTicker(c.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Graceful shutdown: flush whatever is buffered before exiting.
			if len(batch) > 0 {
				c.flush(ctx, batch)
			}
			return nil

		case d, ok := <-deliveries:
			if !ok {
				// Channel closed by broker (e.g. connection lost).
				return nil
			}
			batch = append(batch, d)
			if len(batch) >= c.batchSize {
				c.flush(ctx, batch)
				batch = batch[:0]           // reset without reallocating
				ticker.Reset(c.flushInterval) // restart the timeout after a size-triggered flush
			}

		case <-ticker.C:
			// Timeout flush — drain whatever accumulated since the last flush.
			if len(batch) > 0 {
				c.flush(ctx, batch)
				batch = batch[:0]
			}
		}
	}
}

// flush decodes all deliveries in the batch, bulk-inserts them, then acks or nacks.
//
// Ack strategy: a single Ack(multiple=true) on the last delivery tag acks the
// entire batch in one AMQP frame, matching the single DB round-trip.
//
// Nack strategy:
//   - Malformed JSON → individual Nack(requeue=false) → routed to DLQ.
//   - DB error → Nack(requeue=false) for the whole batch → all go to DLQ.
//     This prevents an infinite retry loop if the DB is persistently unhealthy.
func (c *Consumer) flush(ctx context.Context, deliveries []amqp.Delivery) {
	events := make([]repository.ClickEvent, 0, len(deliveries))

	for _, d := range deliveries {
		var e struct {
			ShortCode string    `json:"short_code"`
			ClickedAt time.Time `json:"clicked_at"`
			IP        string    `json:"ip"`
			Referer   string    `json:"referer"`
		}
		if err := json.Unmarshal(d.Body, &e); err != nil {
			c.logger.Warn("analytics-worker: malformed message, sending to DLQ",
				slog.String("error", err.Error()))
			d.Nack(false, false) // multiple=false (just this one), requeue=false → DLQ
			continue
		}
		events = append(events, repository.ClickEvent{
			ShortCode: e.ShortCode,
			ClickedAt: e.ClickedAt,
			IP:        e.IP,
			Referer:   e.Referer,
		})
	}

	if len(events) == 0 {
		return
	}

	if err := c.repo.BulkInsert(ctx, events); err != nil {
		c.logger.Error("analytics-worker: bulk insert failed, sending batch to DLQ",
			slog.String("error", err.Error()),
			slog.Int("batch_size", len(events)))
		for _, d := range deliveries {
			d.Nack(false, false)
		}
		return
	}

	// One Ack with multiple=true covers every unacked delivery up to and
	// including deliveries[last].DeliveryTag — equivalent to acking each one
	// individually but cheaper (single AMQP frame).
	deliveries[len(deliveries)-1].Ack(true)
	c.logger.Info("analytics-worker: flushed batch", slog.Int("events", len(events)))
}
