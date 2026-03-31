package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/zhejian/url-shortener/analytics-worker/internal/repository"
)

// ErrFatal wraps errors that cannot be resolved by reconnecting to RabbitMQ
// (e.g. a database failure). The caller should not retry after receiving this.
var ErrFatal = errors.New("fatal consumer error")

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
//	    └── queue "analytics.clicks" (quorum, x-dead-letter → dlq)
//	    └── queue "analytics.clicks.dlq" (durable, catch-all for nack'd messages)
//
// The main queue is declared as a quorum queue (Raft-replicated). This requires
// RabbitMQ 3.8+ and means message state is persisted across a quorum of nodes,
// surviving broker restarts without data loss. Queue type cannot be changed after
// creation — requires docker compose down -v to reset if switching from classic.
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

	// Declare the main queue with dead-letter routing and quorum replication:
	// any nack(requeue=false) is forwarded to dlqName via the default exchange ("").
	// x-queue-type=quorum uses Raft consensus to replicate queue state across nodes,
	// ensuring no in-flight messages are lost if RabbitMQ restarts mid-burst.
	args := amqp.Table{
		"x-dead-letter-exchange":    "",       // default exchange routes by queue name
		"x-dead-letter-routing-key": dlqName,  // target queue for dead-lettered messages
		"x-queue-type":              "quorum", // Raft-replicated; survives node restarts
	}
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, args); err != nil {
		return err
	}

	// Bind the main queue to the exchange on the gateway's routing key.
	return ch.QueueBind(queueName, routingKey, exchangeName, false, nil)
}

// Run starts the consume loop. Blocks until ctx is cancelled (graceful shutdown),
// the AMQP channel closes unexpectedly, or a DB error causes a fatal flush failure.
//
// On a DB error, Run returns the error immediately without acking or nacking the
// current batch. The caller (main.go) should exit, which closes the AMQP connection
// and causes RabbitMQ to requeue all unacked messages automatically.
// docker-compose restart: on-failure then restarts the worker to retry.
//
// On shutdown, any buffered un-flushed messages are flushed before returning.
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
			// Graceful shutdown: best-effort flush of whatever is buffered.
			if len(batch) > 0 {
				return c.flush(ctx, batch)
			}
			return nil

		case d, ok := <-deliveries:
			if !ok {
				// Channel closed by broker (e.g. connection lost).
				return nil
			}
			batch = append(batch, d)
			if len(batch) >= c.batchSize {
				if err := c.flush(ctx, batch); err != nil {
					return err // DB error → exit → RabbitMQ requeues unacked messages
				}
				batch = batch[:0]             // reset without reallocating
				ticker.Reset(c.flushInterval) // restart the timeout after a size-triggered flush
			}

		case <-ticker.C:
			// Timeout flush — drain whatever accumulated since the last flush.
			if len(batch) > 0 {
				if err := c.flush(ctx, batch); err != nil {
					return err // DB error → exit → RabbitMQ requeues unacked messages
				}
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
// Nack strategy (two cases):
//   - Malformed JSON → individual Nack(requeue=false) → routed to DLQ.
//     The message itself is broken; no amount of retrying will fix it.
//   - DB error → return the error WITHOUT acking or nacking.
//     The caller exits, the AMQP connection closes, and RabbitMQ automatically
//     requeues all unacked messages. docker-compose restart: on-failure retries
//     once the DB is healthy again — no tight retry loop, no data loss.
func (c *Consumer) flush(ctx context.Context, deliveries []amqp.Delivery) error {
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
			_ = d.Nack(false, false) // multiple=false (just this one), requeue=false → DLQ
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
		return nil
	}

	if err := c.repo.BulkInsert(ctx, events); err != nil {
		c.logger.Error("analytics-worker: bulk insert failed, exiting for restart",
			slog.String("error", err.Error()),
			slog.Int("batch_size", len(events)))
		// Do NOT ack or nack — closing the connection requeues unacked messages.
		// Wrap with ErrFatal so the caller knows not to retry (DB errors require a
		// process restart, not an AMQP reconnect).
		return fmt.Errorf("%w: %w", ErrFatal, err)
	}

	// One Ack with multiple=true covers every unacked delivery up to and
	// including deliveries[last].DeliveryTag — equivalent to acking each one
	// individually but cheaper (single AMQP frame).
	_ = deliveries[len(deliveries)-1].Ack(true)
	c.logger.Info("analytics-worker: flushed batch", slog.Int("events", len(events)))
	return nil
}
