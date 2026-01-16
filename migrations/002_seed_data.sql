-- Migration: 002_seed_data
-- Sample data for development
INSERT INTO
    urls (short_code, original_url, created_by)
VALUES
    ('demo1', 'https://github.com', 'system'),
    ('demo2', 'https://google.com', 'system'),
    ('demo3', 'https://example.com', 'system')
ON CONFLICT (short_code) DO NOTHING;
