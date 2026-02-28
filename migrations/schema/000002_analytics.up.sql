-- Migration: 000002_analytics
-- Create analytics table
CREATE TABLE IF NOT EXISTS analytics (
    id BIGSERIAL PRIMARY KEY,
    short_code VARCHAR(16) NOT NULL,
    clicked_at TIMESTAMP WITH TIME ZONE NOT NULL,
    ip         TEXT,
    referer    TEXT
);

CREATE INDEX IF NOT EXISTS idx_analytics_short_code ON analytics (short_code);

CREATE INDEX IF NOT EXISTS idx_analytics_clicked_at ON analytics (clicked_at);