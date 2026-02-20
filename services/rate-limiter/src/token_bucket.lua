local key         = KEYS[1]
local rate        = tonumber(ARGV[1])  -- tokens per minute
local burst       = tonumber(ARGV[2])  -- max bucket size
local now         = tonumber(ARGV[3])  -- current time in ms (passed from Rust)

local data        = redis.call("HMGET", key, "tokens", "last_refill")
local tokens      = tonumber(data[1]) or burst
local last_refill = tonumber(data[2]) or now

-- Refill: tokens earned since last call
local elapsed     = math.max(0, now - last_refill)
local new_tokens  = math.min(burst, tokens + elapsed * rate / 60000)

local allowed, remaining, retry_after_ms

if new_tokens >= 1 then
    remaining      = math.floor(new_tokens - 1)
    allowed        = 1
    retry_after_ms = 0
else
    remaining      = 0
    allowed        = 0
    retry_after_ms = math.ceil((1 - new_tokens) * 60000 / rate)
end

redis.call("HMSET", key, "tokens", remaining, "last_refill", now)
redis.call("PEXPIRE", key, 120000)

return { allowed, remaining, retry_after_ms }
