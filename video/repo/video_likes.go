package repo

import (
	"context"
	"feedvideo.local/shared-db/cache"
	"feedvideo.local/video/model"
	"fmt"
	"github.com/google/uuid"
	"sort"
	"strconv"
	"strings"
	"time"
)

type LegacyVideoLikeReader interface {
	ReadLegacyVideoLikes(context.Context, string, string) (*model.VideoLikeSnapshot, error)
}

func bootstrapVideoLikes(ctx context.Context, rdb *cache.RedisClient, id, ticket string, confirm bool) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if !validCommentID(id) || !validStatsBootstrapTicket(ticket) || rdb == nil || rdb.Client == nil {
		return ErrLikeUnavailable
	}
	script := prepareCommentLikesLua
	if confirm {
		script = confirmCommentLikesLua
	}
	value, err := rdb.EvalContextBounded(ctx, script, []string{"video_like_state:" + id}, ticket)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLikeUnavailable, err)
	}
	if value != int64(1) {
		return ErrLikeUnavailable
	}
	return nil
}
func (r *VideoRepoMongo) prepareVideoLikes(ctx context.Context, id string) string {
	if r == nil || r.durableLikes {
		return ""
	}
	ticket := uuid.NewString()
	if err := bootstrapVideoLikes(ctx, r.rdb, id, ticket, false); err != nil {
		return ""
	}
	return ticket
}
func (r *VideoRepoMongo) completeVideoLikes(ctx context.Context, id, ticket string) error {
	if r == nil || r.durableLikes {
		return ErrLikeUnavailable
	}
	return bootstrapVideoLikes(ctx, r.rdb, id, ticket, true)
}

func parseVideoLikeSnapshot(value any, id string) (*model.VideoLikeSnapshot, error) {
	values, ok := value.([]interface{})
	if !ok || len(values) != 5 {
		return nil, ErrLikeUnavailable
	}
	parts := make([]string, 5)
	for i, v := range values {
		part, ok := v.(string)
		if !ok {
			return nil, ErrLikeUnavailable
		}
		parts[i] = part
	}
	count, ce := strconv.ParseInt(parts[1], 10, 64)
	version, ve := strconv.ParseInt(parts[2], 10, 64)
	if !validStatsBootstrapTicket(parts[0]) || ce != nil || ve != nil || count < 0 || version < 0 || (parts[3] != "0" && parts[3] != "1") || (parts[4] != "0" && parts[4] != "1") {
		return nil, ErrLikeUnavailable
	}
	return &model.VideoLikeSnapshot{VideoID: id, Epoch: parts[0], Count: count, Version: version, Active: parts[3] == "1", Changed: parts[4] == "1"}, nil
}
func (r *LikeRepoMongo) videoLikeSnapshot(ctx context.Context, id, actor, desired string) (*model.VideoLikeSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !validCommentID(id) || actor != "" && !validLegacyLikeActor(actor) {
		return nil, ErrLikeInvalid
	}
	if r == nil || r.rdb == nil || r.rdb.Client == nil {
		return nil, ErrLikeUnavailable
	}
	// Reuse the confirmed E16 single-hash validation and integer-safe transition.
	value, err := r.rdb.EvalContextBounded(ctx, commentLikesLua, []string{"video_like_state:" + id}, actor, desired)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLikeUnavailable, err)
	}
	return parseVideoLikeSnapshot(value, id)
}
func (r *LikeRepoMongo) ReadLegacyVideoLikes(ctx context.Context, id, actor string) (*model.VideoLikeSnapshot, error) {
	return r.videoLikeSnapshot(ctx, id, actor, "read")
}

const videoLikeMembersLua = "local function snapshot()\n" + commentLikesLua + "\nend\n" + `
local state = snapshot()
if state.err then return state end
local members = {}
for _,field in ipairs(redis.call('HKEYS',KEYS[1])) do
 if string.sub(field,1,7) == 'member:' then
  if redis.call('HGET',KEYS[1],field) ~= '1' then return redis.error_reply('invalid video member') end
  table.insert(members,string.sub(field,8))
 end
end
return {state,members}
`

func (r *LikeRepoMongo) readVideoLikeMembers(ctx context.Context, id string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !validCommentID(id) {
		return nil, ErrLikeInvalid
	}
	if r == nil || r.rdb == nil || r.rdb.Client == nil {
		return nil, ErrLikeUnavailable
	}
	value, err := r.rdb.EvalContextBounded(ctx, videoLikeMembersLua, []string{"video_like_state:" + id}, "", "read")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLikeUnavailable, err)
	}
	values, ok := value.([]interface{})
	if !ok || len(values) != 2 {
		return nil, ErrLikeUnavailable
	}
	snapshot, err := parseVideoLikeSnapshot(values[0], id)
	if err != nil {
		return nil, err
	}
	raw, ok := values[1].([]interface{})
	if !ok || int64(len(raw)) != snapshot.Count {
		return nil, ErrLikeUnavailable
	}
	members := make([]string, 0, len(raw))
	for _, v := range raw {
		actor, ok := v.(string)
		if !ok || !validLegacyLikeActor(actor) || strings.TrimSpace(actor) != actor {
			return nil, ErrLikeUnavailable
		}
		members = append(members, actor)
	}
	sort.Strings(members)
	return members, nil
}
