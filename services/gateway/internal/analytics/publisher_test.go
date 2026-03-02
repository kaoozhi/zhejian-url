package analytics_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zhejian/url-shortener/gateway/internal/analytics"
	"github.com/zhejian/url-shortener/gateway/internal/testutil"
)

var (
	testrabbitmq *testutil.TestRabbitMQ
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	var err error
	testrabbitmq, err = testutil.SetupTestRabbitMQ(ctx)
	if err != nil {
		panic("failed to start RabbitMQ: " + err.Error())
	}

	code := m.Run()
	testrabbitmq.Teardown(ctx)
	os.Exit(code)
}

// When AMQP_URL is empty, NewPublisher returns nil without error.
func TestNewPublisher_Disabled(t *testing.T) {
	p, err := analytics.NewPublisher("", nil)
	assert.NoError(t, err)
	assert.Nil(t, p)
}

// When a valid AMQP URL is given, NewPublisher connects and IsConnected is true.
func TestNewPublisher_Connected(t *testing.T) {
	ctx := context.Background()
	amqpURL, err := testrabbitmq.AmqpURL(ctx)
	require.NoError(t, err)

	pub, err := analytics.NewPublisher(amqpURL, slog.Default())
	require.NoError(t, err)
	require.NotNil(t, pub)
	defer pub.Close()

	assert.True(t, pub.IsConnected())
}

// Nil and degraded publishers must not deliver events to the broker.
// Verified by consuming from the exchange and confirming no message arrives.
func TestPublish_DroppedEvents(t *testing.T) {
	ctx := context.Background()
	amqpURL, err := testrabbitmq.AmqpURL(ctx)
	require.NoError(t, err)

	conn, err := amqp.Dial(amqpURL)
	require.NoError(t, err)
	defer conn.Close()
	ch, err := conn.Channel()
	require.NoError(t, err)
	defer ch.Close()

	// Ensure the exchange exists (publisher declares it on connect).
	require.NoError(t, ch.ExchangeDeclare("analytics", "topic", true, false, false, false, nil))
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	require.NoError(t, err)
	require.NoError(t, ch.QueueBind(q.Name, "analytics.clicks", "analytics", false, nil))
	msgs, err := ch.Consume(q.Name, "", true, false, false, false, nil)
	require.NoError(t, err)

	event := analytics.ClickEvent{ShortCode: "drop-test", ClickedAt: time.Now(), IP: "1.2.3.4"}

	var nilPub *analytics.Publisher
	nilPub.Publish(ctx, event)

	degraded := analytics.NewDegradedPublisher(slog.Default())
	degraded.Publish(ctx, event)

	select {
	case <-msgs:
		t.Fatal("nil or degraded publisher must not deliver messages to broker")
	case <-time.After(300 * time.Millisecond):
		// expected: both events were silently dropped
	}
}

// Publisher reconnects after broker restart and resumes message delivery.
// StopApp/StartApp are used instead of container stop/start to preserve the
// host port mapping (and thus the AMQP URL) across the outage.
func TestPublish_Reconnect(t *testing.T) {
	ctx := context.Background()
	amqpURL, err := testrabbitmq.AmqpURL(ctx)
	require.NoError(t, err)

	pub, err := analytics.NewPublisher(amqpURL, slog.Default())
	require.NoError(t, err)
	require.NotNil(t, pub)
	defer pub.Close()

	// Verify publish works against a healthy broker before inducing the outage.
	pub.Publish(ctx, analytics.ClickEvent{ShortCode: "abc", ClickedAt: time.Now(), IP: "1.2.3.4"})

	// Simulate broker crash: stop_app closes all AMQP connections.
	require.NoError(t, testrabbitmq.StopApp(ctx))

	// Publish during outage: event is dropped and a warning is logged.
	pub.Publish(ctx, analytics.ClickEvent{ShortCode: "abc", ClickedAt: time.Now(), IP: "1.2.3.4"})

	require.NoError(t, testrabbitmq.StartApp(ctx))

	// Poll until watchAndReconnect re-establishes the channel.
	assert.Eventually(t, pub.IsConnected, 30*time.Second, 500*time.Millisecond,
		"publisher did not reconnect within 30s")

	// Spy consumer: exclusive auto-delete queue bound to the analytics exchange.
	conn, err := amqp.Dial(amqpURL)
	require.NoError(t, err)
	defer conn.Close()
	ch, err := conn.Channel()
	require.NoError(t, err)
	defer ch.Close()
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	require.NoError(t, err)
	err = ch.QueueBind(q.Name, "analytics.clicks", "analytics", false, nil)
	require.NoError(t, err)
	msgs, err := ch.Consume(q.Name, "", true, false, false, false, nil)
	require.NoError(t, err)

	pub.Publish(ctx, analytics.ClickEvent{ShortCode: "abc", ClickedAt: time.Now(), IP: "1.2.3.4"})

	select {
	case <-msgs:
	case <-time.After(3 * time.Second):
		t.Fatal("publisher did not recover: no message received within 3s")
	}
}
