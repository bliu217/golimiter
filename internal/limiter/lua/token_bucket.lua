local capacity = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
local now_override_us = tonumber(ARGV[4])

local now_us
if now_override_us ~= nil and now_override_us >= 0 then
  now_us = now_override_us
else
  local t = redis.call("TIME")
  now_us = tonumber(t[1]) * 1000000 + tonumber(t[2])
end

local state = redis.call("HMGET", KEYS[1], "tokens", "last_refill_us")
local tokens = tonumber(state[1])
local last_refill_us = tonumber(state[2])

if tokens == nil or last_refill_us == nil then
  tokens = capacity
  last_refill_us = now_us
end

local elapsed_us = now_us - last_refill_us
if elapsed_us < 0 then
  elapsed_us = 0
end

if elapsed_us > 0 then
  local refill_tokens = (elapsed_us / 1000000.0) * refill_rate
  tokens = math.min(capacity, tokens + refill_tokens)
  last_refill_us = now_us
end

local allowed = 0
if tokens >= cost then
  tokens = tokens - cost
  allowed = 1
end

redis.call("HSET", KEYS[1], "tokens", tostring(tokens), "last_refill_us", tostring(last_refill_us))

local ttl_ms = math.ceil((capacity / refill_rate) * 1000) * 2
if ttl_ms < 1 then
  ttl_ms = 1
end
redis.call("PEXPIRE", KEYS[1], ttl_ms)

return {allowed, tostring(tokens)}
