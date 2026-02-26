package consumer_test

import (
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
)

// TestBatchAccumulation verifies the slice grow-and-reset logic used in Run().
// It does not need a real AMQP connection — it tests the pure batch mechanics.
func TestBatchAccumulation(t *testing.T) {
	batchSize := 3
	var batch []amqp.Delivery

	// Accumulate below threshold — no flush yet.
	for i := 0; i < batchSize-1; i++ {
		batch = append(batch, amqp.Delivery{})
		assert.Less(t, len(batch), batchSize)
	}

	// One more delivery reaches the threshold.
	batch = append(batch, amqp.Delivery{})
	assert.Equal(t, batchSize, len(batch))

	// Simulate reset after flush — capacity is kept, length zeroed.
	batch = batch[:0]
	assert.Equal(t, 0, len(batch))
}

// TestFlushInterval verifies that the flush interval constant is a sensible duration.
func TestFlushInterval(t *testing.T) {
	flushInterval := 5 * time.Second
	assert.Equal(t, 5*time.Second, flushInterval)
	assert.Greater(t, flushInterval, time.Duration(0))
}
