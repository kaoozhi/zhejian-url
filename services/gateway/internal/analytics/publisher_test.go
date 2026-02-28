package analytics_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/zhejian/url-shortener/gateway/internal/analytics"
)

// When AMQP_URL is empty, NewPublisher returns nil without error.
func TestNewPublisher_Disabled(t *testing.T) {
	p, err := analytics.NewPublisher("", nil)
	assert.NoError(t, err)
	assert.Nil(t, p)
}

// A nil publisher's Publish is a no-op (safe to call when disabled).
func TestPublish_NilPublisher(t *testing.T) {
	var p *analytics.Publisher
	// should not panic
	p.Publish(context.Background(), analytics.ClickEvent{
		ShortCode: "abc",
		ClickedAt: time.Now(),
		IP:        "1.2.3.4",
	})
}
