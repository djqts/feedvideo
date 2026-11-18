package repo

import (
	"context"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"time"
)

type RefreshStore struct{ rdb *redis.Client }

func NewRefreshStore(rdb *redis.Client) *RefreshStore { return &RefreshStore{rdb: rdb} }

const refreshTokenPrefix = "jwt_refresh:"
const RefreshRevokedPrefix = "jwt_refresh_revoked:"
const refreshUsedPrefix = "jwt_refresh_used:"
const refreshFamilyPrefix = "jwt_refresh_family:"

var ErrTokenReused = errors.New("refresh token reused")
var ErrInvalidRefreshToken = errors.New("refresh token invalid")

func refreshTTLMillis(expiresAt int64) (int64, error) {
	ttl := time.Until(time.Unix(expiresAt, 0))
	if ttl <= 0 {
		return 0, fmt.Errorf("expiresAt is in the past")
	}
	return int64((ttl + time.Millisecond - 1) / time.Millisecond), nil
}

// A family records the user revocation generation observed by a password-verified
// login. New logins can recover from reuse without restoring older tokens.
// The legacy marker "1" remains unconditional (also used by the ban hook).
func (s *RefreshStore) Save(userID, tokenDigest string, expiresAt int64) error {
	ttl, err := refreshTTLMillis(expiresAt)
	if err != nil {
		return err
	}
	const script = `
local revoked = redis.call('GET', KEYS[3]) or ''
if revoked ~= '' and string.sub(revoked, 1, 6) ~= 'reuse:' then return 0 end
redis.call('SET', KEYS[2], 'active:' .. revoked, 'PX', ARGV[1])
redis.call('SET', KEYS[1], 'family:' .. ARGV[2], 'PX', ARGV[1])
return 1
`
	n, err := s.rdb.Eval(context.Background(), script, []string{
		refreshTokenPrefix + userID + ":" + tokenDigest,
		refreshFamilyPrefix + userID + ":" + tokenDigest,
		RefreshRevokedPrefix + userID,
	}, ttl, tokenDigest).Int64()
	if err != nil {
		return err
	}
	if n != 1 {
		return redis.Nil
	}
	return nil
}

// Validate applies the same fail-closed user/family checks as Rotate.
func (s *RefreshStore) Validate(userID, tokenDigest string) error {
	const script = `
local revoked = redis.call('GET', KEYS[2]) or ''
local value = redis.call('GET', KEYS[1])
if not value then return 0 end
if value == '1' then
  if revoked ~= '' then return 0 end
  local legacyState = redis.call('GET', ARGV[1] .. ARGV[2])
  if not legacyState or legacyState == 'active:' then return 1 else return 0 end
end
if string.sub(value, 1, 7) ~= 'family:' then return 0 end
local state = redis.call('GET', ARGV[1] .. string.sub(value, 8))
if not state or string.sub(state, 1, 7) ~= 'active:' then return 0 end
if revoked ~= '' and state ~= 'active:' .. revoked then return 0 end
return 1
`
	n, err := s.rdb.Eval(context.Background(), script, []string{
		refreshTokenPrefix + userID + ":" + tokenDigest, RefreshRevokedPrefix + userID,
	}, refreshFamilyPrefix+userID+":", tokenDigest).Int64()
	if err != nil {
		return err
	}
	if n != 1 {
		return redis.Nil
	}
	return nil
}

// Rotate checks revocation and persists successor/used/family state in one script.
// New tokens require positive family state even after eviction. Genuine reuse
// retains the existing user-wide policy; random tokens never revoke.
func (s *RefreshStore) Rotate(userID, oldDigest, newDigest string, expiresAt int64) error {
	ttl, err := refreshTTLMillis(expiresAt)
	if err != nil {
		return err
	}
	const script = `
local revoked = redis.call('GET', KEYS[4]) or ''
if revoked ~= '' and string.sub(revoked, 1, 6) ~= 'reuse:' then return 3 end
local value = redis.call('GET', KEYS[1])
if not value then
  local used = redis.call('GET', KEYS[3])
  if not used then return 0 end
  local duration = math.max(tonumber(ARGV[1]), redis.call('PTTL', KEYS[3]), 604800000)
  local familyKey = nil
  if string.sub(used, 1, 7) == 'family:' then
    familyKey = ARGV[3] .. string.sub(used, 8)
    local state = redis.call('GET', familyKey)
    if not state or string.sub(state, 1, 7) ~= 'active:' then return 3 end
    if revoked ~= '' and state ~= 'active:' .. revoked then return 3 end
    duration = math.max(duration, redis.call('PTTL', familyKey))
  elseif used ~= '1' then return 3 end
  -- Scripts do not roll back: first deny every older user generation, then
  -- retain the family tombstone even if the global marker is later evicted.
  redis.call('SET', KEYS[4], 'reuse:' .. ARGV[2], 'PX', duration)
  if familyKey then redis.call('SET', familyKey, 'revoked', 'PX', duration) end
  return 2
end
local root = ARGV[4]
local state = 'active:' .. revoked
if value == '1' then
  if revoked ~= '' then return 3 end
  local legacyState = redis.call('GET', ARGV[3] .. root)
  if legacyState and legacyState ~= 'active:' then return 3 end
elseif string.sub(value, 1, 7) == 'family:' then
  root = string.sub(value, 8)
  state = redis.call('GET', ARGV[3] .. root)
  if not state or string.sub(state, 1, 7) ~= 'active:' then return 3 end
  if revoked ~= '' and state ~= 'active:' .. revoked then return 3 end
else return 3 end
local familyKey = ARGV[3] .. root
local familyTTL = math.max(tonumber(ARGV[1]), redis.call('PTTL', familyKey))
redis.call('SET', familyKey, state, 'PX', familyTTL)
redis.call('SET', KEYS[2], 'family:' .. root, 'PX', ARGV[1])
redis.call('SET', KEYS[3], 'family:' .. root, 'PX', ARGV[1])
redis.call('DEL', KEYS[1])
return 1
`
	n, err := s.rdb.Eval(context.Background(), script, []string{
		refreshTokenPrefix + userID + ":" + oldDigest,
		refreshTokenPrefix + userID + ":" + newDigest,
		refreshUsedPrefix + userID + ":" + oldDigest,
		RefreshRevokedPrefix + userID,
	}, ttl, newDigest, refreshFamilyPrefix+userID+":", oldDigest).Int64()
	if err != nil {
		return err
	}
	switch n {
	case 1:
		return nil
	case 2:
		return ErrTokenReused
	case 0:
		return ErrInvalidRefreshToken
	case 3:
		return redis.Nil
	}
	return fmt.Errorf("unexpected rotate result")
}

// Delete revokes successors already rotated from the logout cookie. Only legacy
// used=1 (no lineage) falls back to the existing user-wide revocation policy.
func (s *RefreshStore) Delete(userID, tokenDigest string) error {
	const script = `
local value = redis.call('GET', KEYS[1])
local used = redis.call('GET', KEYS[2])
local duration = math.max(redis.call('PTTL', KEYS[1]), redis.call('PTTL', KEYS[2]), 1)
if not value and not used then return 0 end
local root = ARGV[2]
if not value and used == '1' then
  local revoked = redis.call('GET', KEYS[3]) or ''
  if revoked == '' or string.sub(revoked, 1, 6) == 'reuse:' then
    redis.call('SET', KEYS[3], 'reuse:' .. ARGV[2], 'PX', math.max(duration, 604800000))
  end
  return 1
end
local recorded = value or used
if recorded ~= '1' then
  if string.sub(recorded, 1, 7) ~= 'family:' then return 0 end
  root = string.sub(recorded, 8)
end
local familyKey = ARGV[1] .. root
local state = redis.call('GET', familyKey)
duration = math.max(duration, redis.call('PTTL', familyKey))
redis.call('SET', familyKey, 'revoked', 'PX', duration)
redis.call('SET', KEYS[2], 'family:' .. root, 'PX', duration)
redis.call('DEL', KEYS[1])
return 1
`
	n, err := s.rdb.Eval(context.Background(), script, []string{
		refreshTokenPrefix + userID + ":" + tokenDigest, refreshUsedPrefix + userID + ":" + tokenDigest,
		RefreshRevokedPrefix + userID,
	}, refreshFamilyPrefix+userID+":", tokenDigest).Int64()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInvalidRefreshToken
	}
	return nil
}
