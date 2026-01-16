-- URL Shortener Database Schema

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- URLs table
CREATE TABLE urls (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    short_code VARCHAR(16) UNIQUE NOT NULL,
    original_url TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE,
    click_count BIGINT DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE,
    created_by VARCHAR(255),
    metadata JSONB DEFAULT '{}'::jsonb
);

-- Indexes for fast lookups
CREATE INDEX idx_urls_short_code ON urls(short_code) WHERE is_active = TRUE;
CREATE INDEX idx_urls_created_at ON urls(created_at DESC);
CREATE INDEX idx_urls_expires_at ON urls(expires_at) WHERE expires_at IS NOT NULL;

-- -- Click analytics table
-- CREATE TABLE url_clicks (
--     id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
--     url_id UUID NOT NULL REFERENCES urls(id) ON DELETE CASCADE,
--     clicked_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
--     ip_address INET,
--     user_agent TEXT,
--     referer TEXT,
--     country_code VARCHAR(2),
--     device_type VARCHAR(20)
-- );

-- -- Indexes for analytics queries
-- CREATE INDEX idx_clicks_url_id ON url_clicks(url_id);
-- CREATE INDEX idx_clicks_clicked_at ON url_clicks(clicked_at DESC);
-- CREATE INDEX idx_clicks_country ON url_clicks(country_code) WHERE country_code IS NOT NULL;

-- -- Rate limit tracking (for persistence across restarts)
-- CREATE TABLE rate_limits (
--     key VARCHAR(255) PRIMARY KEY,
--     tokens DECIMAL(10, 4) NOT NULL,
--     last_updated TIMESTAMP WITH TIME ZONE DEFAULT NOW()
-- );

-- -- Function to update updated_at timestamp
-- CREATE OR REPLACE FUNCTION update_updated_at_column()
-- RETURNS TRIGGER AS $$
-- BEGIN
--     NEW.updated_at = NOW();
--     RETURN NEW;
-- END;
-- $$ language 'plpgsql';

-- -- Trigger for urls table
-- CREATE TRIGGER update_urls_updated_at
--     BEFORE UPDATE ON urls
--     FOR EACH ROW
--     EXECUTE FUNCTION update_updated_at_column();

-- -- Function to increment click count
-- CREATE OR REPLACE FUNCTION increment_click_count()
-- RETURNS TRIGGER AS $$
-- BEGIN
--     UPDATE urls SET click_count = click_count + 1 WHERE id = NEW.url_id;
--     RETURN NEW;
-- END;
-- $$ language 'plpgsql';

-- -- Trigger to auto-increment click count
-- CREATE TRIGGER trigger_increment_click_count
--     AFTER INSERT ON url_clicks
--     FOR EACH ROW
--     EXECUTE FUNCTION increment_click_count();

-- Sample data for development
INSERT INTO urls (short_code, original_url, created_by) VALUES
    ('demo1', 'https://github.com', 'system'),
    ('demo2', 'https://google.com', 'system'),
    ('demo3', 'https://example.com', 'system');
