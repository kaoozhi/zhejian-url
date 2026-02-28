package analytics

import "time"

// ClickEvent represents a single redirect click published to RabbitMQ.
// It is serialised as JSON in the message body.
type ClickEvent struct {
	ShortCode string    `json:"short_code"`
	ClickedAt time.Time `json:"clicked_at"`
	IP        string    `json:"ip"`
	Referer   string    `json:"referer"`
}
