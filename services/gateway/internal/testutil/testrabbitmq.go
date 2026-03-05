package testutil

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestRabbitMQ holds the RabbitMQ test container and its connection details.
type TestRabbitMQ struct {
	container *rabbitmq.RabbitMQContainer
}

// SetupTestRabbitMQ creates and starts a RabbitMQ container.
// The caller is responsible for calling Teardown when done.
func SetupTestRabbitMQ(ctx context.Context) (*TestRabbitMQ, error) {
	container, err := rabbitmq.Run(ctx,
		"rabbitmq:3.12.11-management-alpine",
		rabbitmq.WithAdminUsername("testmq"),
		rabbitmq.WithAdminPassword("testmq"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("Server startup complete"),
			).WithDeadline(60*time.Second),
		),
	)
	if err != nil {
		return nil, err
	}

	return &TestRabbitMQ{container: container}, nil
}

// AmqpURL returns the AMQP connection URL for this container.
// The URL includes credentials and the mapped host port.
func (t *TestRabbitMQ) AmqpURL(ctx context.Context) (string, error) {
	return t.container.AmqpURL(ctx)
}

// StopApp stops the RabbitMQ application layer (disconnects all AMQP clients)
// while keeping the container running. Port mappings are preserved so the
// original AMQP URL remains valid after StartApp.
func (t *TestRabbitMQ) StopApp(ctx context.Context) error {
	exitCode, _, err := t.container.Exec(ctx, []string{"rabbitmqctl", "stop_app"})
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("rabbitmqctl stop_app exited with code %d", exitCode)
	}
	return nil
}

// StartApp resumes the RabbitMQ application layer after StopApp.
func (t *TestRabbitMQ) StartApp(ctx context.Context) error {
	exitCode, _, err := t.container.Exec(ctx, []string{"rabbitmqctl", "start_app"})
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("rabbitmqctl start_app exited with code %d", exitCode)
	}
	return nil
}

// Stop pauses the container to simulate a broker crash.
func (t *TestRabbitMQ) Stop(ctx context.Context) error {
	timeout := 1 * time.Second
	return t.container.Stop(ctx, &timeout)
}

// Start resumes a stopped container.
func (t *TestRabbitMQ) Start(ctx context.Context) error {
	return t.container.Start(ctx)
}

// Teardown terminates the container and releases all resources.
func (t *TestRabbitMQ) Teardown(ctx context.Context) {
	if t.container != nil {
		_ = t.container.Terminate(ctx)
	}
}
