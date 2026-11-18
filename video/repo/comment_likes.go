package repo

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type LegacyCommentLikeReader interface {
	ReadLegacyLikes(context.Context, string, string) (*model.CommentLikeSnapshot, error)
}

const prepareCommentLikesLua = `
if redis.call('TYPE', KEYS[1]).ok ~= 'none' then return 0 end
redis.call('HSET', KEYS[1], 'pending', ARGV[1])
return 1
`
const confirmCommentLikesLua = `
if redis.call('TYPE', KEYS[1]).ok ~= 'hash' or redis.call('HLEN', KEYS[1]) ~= 1 then return 0 end
if redis.call('HGET', KEYS[1], 'pending') ~= ARGV[1] then return 0 end
redis.call('HSET', KEYS[1], 'epoch', ARGV[1], 'ready', '1', 'count', '0', 'version', '0')
redis.call('HDEL', KEYS[1], 'pending')
return 1
`

// Members, counters and the proof share one key, so eviction loses them together.
const commentLikesLua = `
local function number(s)
 if not s or (s ~= '0' and not string.match(s, '^[1-9]%d*$')) then return false end
 return string.len(s) < 19 or (string.len(s) == 19 and s <= '9223372036854775807')
end
if redis.call('TYPE', KEYS[1]).ok ~= 'hash' then return redis.error_reply('comment likes unknown') end
local epoch = redis.call('HGET', KEYS[1], 'epoch')
local count = redis.call('HGET', KEYS[1], 'count')
local version = redis.call('HGET', KEYS[1], 'version')
if redis.call('HGET', KEYS[1], 'ready') ~= '1' or not epoch or string.len(epoch) ~= 36 then return redis.error_reply('comment likes unconfirmed') end
if string.sub(epoch,9,9) ~= '-' or string.sub(epoch,14,14) ~= '-' or string.sub(epoch,19,19) ~= '-' or string.sub(epoch,24,24) ~= '-' or string.sub(epoch,15,15) ~= '4' or not string.match(string.sub(epoch,20,20),'^[89ab]$') then return redis.error_reply('invalid epoch') end
local hex = string.gsub(epoch,'-','')
if string.len(hex) ~= 32 or not string.match(hex,'^[0-9a-f]+$') or not number(count) or not number(version) then return redis.error_reply('invalid comment likes') end
if count ~= tostring(redis.call('HLEN', KEYS[1]) - 4) then return redis.error_reply('incomplete comment members') end
local field = 'member:' .. ARGV[1]
local member = redis.call('HGET', KEYS[1], field)
if member and member ~= '1' then return redis.error_reply('invalid comment member') end
local active = member and '1' or '0'
local changed = '0'
if ARGV[2] ~= 'read' and ARGV[2] ~= active then
 if version == '9223372036854775807' then return redis.error_reply('comment version exhausted') end
 local delta = ARGV[2] == '1' and 1 or -1
 redis.call('HINCRBY', KEYS[1], 'count', delta)
 redis.call('HINCRBY', KEYS[1], 'version', 1)
 if ARGV[2] == '1' then redis.call('HSET', KEYS[1], field, '1') else redis.call('HDEL', KEYS[1], field) end
 active, changed = ARGV[2], '1'
 count, version = redis.call('HGET', KEYS[1], 'count'), redis.call('HGET', KEYS[1], 'version')
end
return {epoch,count,version,active,changed}
`

func (r *CommentRepoMongo) bootstrapCommentLikes(ctx context.Context, id, epoch string, confirm bool) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if r == nil || r.rdb == nil || r.rdb.Client == nil {
		return ErrLikeUnavailable
	}
	script := prepareCommentLikesLua
	if confirm {
		script = confirmCommentLikesLua
	}
	value, err := r.rdb.EvalContextBounded(ctx, script, []string{"comment_like_state:" + id}, epoch)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLikeUnavailable, err)
	}
	if value != int64(1) {
		return ErrLikeUnavailable
	}
	return nil
}

// A successful driver InsertOne returns the generated ID even for an n=0
// reply. Inspect the actual acknowledged insert response before confirming zeros.
func (r *CommentRepoMongo) insertNewComment(ctx context.Context, comment *model.Comment) error {
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return ErrCommentUnavailable
	}
	db := r.mdb.Col().Database()
	command := bson.D{{Key: "insert", Value: r.mdb.Col().Name()}, {Key: "documents", Value: bson.A{comment}}, {Key: "ordered", Value: true}}
	if wc := db.WriteConcern(); wc != nil {
		if !wc.Acknowledged() {
			return ErrCommentUnavailable
		}
		command = append(command, bson.E{Key: "writeConcern", Value: wc})
	}
	raw, err := db.RunCommand(ctx, command).Raw()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCommentUnavailable, err)
	}
	n, ok := raw.Lookup("n").Int32OK()
	if !ok {
		value, valid := raw.Lookup("n").Int64OK()
		if !valid || value != 1 {
			return ErrCommentUnavailable
		}
	} else if n != 1 {
		return ErrCommentUnavailable
	}
	if _, err := raw.LookupErr("writeConcernError"); err == nil {
		return ErrCommentUnavailable
	}
	if value, err := raw.LookupErr("writeErrors"); err == nil {
		array, valid := value.ArrayOK()
		if !valid {
			return ErrCommentUnavailable
		}
		values, err := array.Values()
		if err != nil || len(values) != 0 {
			return ErrCommentUnavailable
		}
	}
	return nil
}

func (r *CommentRepoMongo) commentLikeSnapshot(ctx context.Context, id, actor, desired string) (*model.CommentLikeSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !validCommentID(id) || actor != "" && !validLegacyLikeActor(actor) {
		return nil, ErrLikeInvalid
	}
	if r == nil || r.rdb == nil || r.rdb.Client == nil {
		return nil, ErrLikeUnavailable
	}
	value, err := r.rdb.EvalContextBounded(ctx, commentLikesLua, []string{"comment_like_state:" + id}, actor, desired)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLikeUnavailable, err)
	}
	values, ok := value.([]interface{})
	if !ok || len(values) != 5 {
		return nil, ErrLikeUnavailable
	}
	strings := make([]string, 5)
	for i, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, ErrLikeUnavailable
		}
		strings[i] = text
	}
	count, countErr := strconv.ParseInt(strings[1], 10, 64)
	version, versionErr := strconv.ParseInt(strings[2], 10, 64)
	if !validStatsBootstrapTicket(strings[0]) || countErr != nil || versionErr != nil || count < 0 || version < 0 || (strings[3] != "0" && strings[3] != "1") || (strings[4] != "0" && strings[4] != "1") {
		return nil, ErrLikeUnavailable
	}
	return &model.CommentLikeSnapshot{CommentID: id, Epoch: strings[0], Count: count, Version: version, Active: strings[3] == "1", Changed: strings[4] == "1"}, nil
}

func (r *CommentRepoMongo) ReadLegacyLikes(ctx context.Context, id, actor string) (*model.CommentLikeSnapshot, error) {
	return r.commentLikeSnapshot(ctx, id, actor, "read")
}

func (r *CommentRepoMongo) setLegacyCommentLike(ctx context.Context, id, actor string, active bool) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !validLegacyLikeActor(actor) {
		return ErrLikeInvalid
	}
	desired := "0"
	if active {
		desired = "1"
	}
	snapshot, err := r.commentLikeSnapshot(ctx, id, actor, desired)
	if err != nil {
		return err
	}
	if ctx.Err() == nil && snapshot.Version > 0 {
		if err := r.projectLegacyCommentLikes(ctx, snapshot); err != nil {
			slog.Warn("committed comment like projection unavailable", "comment_id", id, "error", err)
		}
	}
	return nil
}

func (r *CommentRepoMongo) projectLegacyCommentLikes(ctx context.Context, snapshot *model.CommentLikeSnapshot) error {
	if snapshot == nil || !validCommentID(snapshot.CommentID) || !validStatsBootstrapTicket(snapshot.Epoch) || snapshot.Version < 1 || snapshot.Count < 0 || r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return ErrLikeUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	oid, _ := primitive.ObjectIDFromHex(snapshot.CommentID)
	filter := bson.M{"_id": oid, "legacy_like_epoch": snapshot.Epoch, "legacy_like_version": bson.M{"$lt": snapshot.Version}, "$expr": bson.M{"$and": bson.A{
		bson.M{"$eq": bson.A{bson.M{"$type": "$legacy_like_epoch"}, "string"}},
		bson.M{"$eq": bson.A{bson.M{"$type": "$legacy_like_version"}, "long"}},
	}}}
	result, err := r.mdb.UpdateOne(ctx, filter, bson.M{"$set": bson.M{"like_count": snapshot.Count, "legacy_like_version": snapshot.Version}}, options.Update().SetUpsert(false))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLikeUnavailable, err)
	}
	if result == nil || result.UpsertedCount != 0 || result.MatchedCount > 1 || result.ModifiedCount > 1 || result.MatchedCount == 1 && result.ModifiedCount != 1 {
		return ErrLikeUnavailable
	}
	return nil
}
