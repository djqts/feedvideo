// Package legacymetrics reads bounded legacy Redis observations, never business facts.
package legacymetrics

import (
	"context"
	"errors"
	"strconv"
	"time"
)

type Value struct {
	Value int64
	Known bool
}
type Snapshot struct {
	Succeeded, Failed, Retried, DeadLettered, Reconcile Value
	Tasks, Processing, DLQ, InvalidValues               int64
}
type EvalReader interface {
	EvalContextBounded(context.Context, string, []string, ...interface{}) (interface{}, error)
}

const ReadBudget = 500 * time.Millisecond

// Strings are length-checked before GET, so a corrupt large cache value does
// not create an unbounded result. Counters stay strings through Lua/RESP.
const optionalLua = `
local function optional(key)
 local kind=redis.call('TYPE',key).ok
 if kind=='none' then return {0,''} end
 if kind~='string' then return {2,''} end
 local length=redis.call('STRLEN',key)
 if length<1 or length>19 then return {2,''} end
 return {1,redis.call('GET',key)}
end
local function append(out,key)
 local value=optional(key)
 out[#out+1]=value[1]; out[#out+1]=value[2]
end
`
const gatewayLua = optionalLua + `
local out={1,0,0,0}
if #KEYS==5 then
 for i=1,5 do append(out,KEYS[i]) end
else
 for i=1,4 do out[#out+1]=0; out[#out+1]='' end
 append(out,KEYS[1])
end
return out
`
const mediaLua = optionalLua + `
local out={1,redis.call('LLEN',KEYS[1]),redis.call('LLEN',KEYS[2]),redis.call('LLEN',KEYS[3])}
for i=4,7 do append(out,KEYS[i]) end
out[#out+1]=0; out[#out+1]=''
return out
`

func ReadGateway(ctx context.Context, reader EvalReader, legacy bool) (Snapshot, error) {
	keys := []string{"metrics:reconcile:stats_diff"}
	if legacy {
		keys = []string{"metrics:media:succeeded", "metrics:media:failed", "metrics:media:retried", "metrics:media:dlq", "metrics:reconcile:stats_diff"}
	}
	return read(ctx, reader, gatewayLua, keys)
}
func ReadMedia(ctx context.Context, reader EvalReader) (Snapshot, error) {
	return read(ctx, reader, mediaLua, []string{"media:tasks", "media:processing", "media:dlq", "metrics:media:succeeded", "metrics:media:failed", "metrics:media:retried", "metrics:media:dlq"})
}

func read(ctx context.Context, reader EvalReader, script string, keys []string) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, ReadBudget)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if reader == nil {
		return Snapshot{}, errors.New("legacy snapshot unavailable")
	}
	result, err := reader.EvalContextBounded(ctx, script, keys)
	if ctx.Err() != nil {
		return Snapshot{}, ctx.Err()
	}
	if err != nil {
		return Snapshot{}, err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != 14 {
		return Snapshot{}, errors.New("invalid legacy snapshot protocol")
	}
	version, ok := values[0].(int64)
	if !ok || version != 1 {
		return Snapshot{}, errors.New("invalid legacy snapshot version")
	}
	var snap Snapshot
	for i, field := range []*int64{&snap.Tasks, &snap.Processing, &snap.DLQ} {
		value, ok := values[i+1].(int64)
		if !ok || value < 0 {
			return Snapshot{}, errors.New("invalid legacy queue result")
		}
		*field = value
	}
	for i, field := range []*Value{&snap.Succeeded, &snap.Failed, &snap.Retried, &snap.DeadLettered, &snap.Reconcile} {
		state, ok := values[4+i*2].(int64)
		raw, rawOK := values[5+i*2].(string)
		if !ok || !rawOK || state < 0 || state > 2 || (state != 1 && raw != "") {
			return Snapshot{}, errors.New("invalid legacy value protocol")
		}
		if state == 0 {
			continue
		}
		if state == 2 {
			snap.InvalidValues++
			continue
		}
		value, valid := parseValue(raw)
		if !valid {
			snap.InvalidValues++
			continue
		}
		*field = Value{value, true}
	}
	return snap, nil
}

func parseValue(raw string) (int64, bool) {
	if len(raw) < 1 || len(raw) > 19 {
		return 0, false
	}
	for i := range raw {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil
}
