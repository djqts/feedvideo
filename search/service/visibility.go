package service

import (
	"context"
	"sync"
	"time"

	feedmodel "feedvideo.local/search/model"
	feedrepo "feedvideo.local/search/repo"
	usermodel "feedvideo.local/user/model"
	videomodel "feedvideo.local/video/model"
	videorepo "feedvideo.local/video/repo"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func (s *SearchService) searchCurrent(ctx context.Context, q, typ string, page, size int64, cursor string) (*feedmodel.SearchResp, error) {
	state, err := decodeCandidateCursor(q, typ, page, size, cursor, s.candidateIndex())
	if err != nil {
		return nil, err
	}
	q = feedrepo.Normalize(q)
	resp := emptySearchResponse(typ)
	if resp.Categories.Video != nil {
		resp.Categories.Video.TotalRelation = "estimate"
		resp.TotalRelation = "estimate"
	}
	if q == "" {
		return resp, nil
	}
	// Cache, all candidate providers and the final source gate share one parent.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	key := s.candidateCacheKey(q, typ, page, size, cursor, state)
	var cached *candidateEnvelope
	if s.repo != nil {
		raw, _ := s.repo.GetCache(ctx, key)
		cached = s.readCandidateCache(raw, q, typ, page, size, state, cursor)
	}
	if ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	provider, _ := s.videoRepo.(videorepo.SearchCandidateReader)
	var videoPage *videorepo.SearchCandidatePage
	var tagCandidates []*videomodel.Video
	var tagTotal int64
	var videoErr, userErr, tagErr error
	var wg sync.WaitGroup
	do := func(fn func()) { wg.Add(1); go func() { defer wg.Done(); fn() }() }
	if resp.Categories.Video != nil {
		do(func() {
			if cached != nil {
				videoPage = cached.Video
				return
			}
			if provider == nil {
				videoErr = ErrUnavailable
				return
			}
			limit := boundedLimit(state.VideoOffset, videoWindow(typ, size))
			videoPage, videoErr = provider.SearchCandidates(ctx, q, state.VideoOffset, limit)
			if videoErr == nil && !validCandidatePage(videoPage, state.VideoOffset, limit) {
				videoErr = ErrUnavailable
			}
		})
	}
	if resp.Categories.User != nil {
		do(func() {
			if cached != nil {
				copy := *cached.User
				resp.Categories.User = &copy
				return
			}
			if s.userSvc == nil {
				userErr = ErrUnavailable
				return
			}
			var users *usermodel.SearchResponse
			users, userErr = s.userSvc.SearchUsers(ctx, q, int(page), int(size))
			if userErr != nil {
				return
			}
			if users != nil {
				if users.Total < 0 {
					userErr = ErrUnavailable
					return
				}
				resp.Categories.User.Total = int64(users.Total)
				resp.Categories.User.HasMore = page*size < int64(users.Total)
				if items, ok := users.Items.([]*usermodel.UserDocument); ok {
					if int64(len(items)) > size {
						userErr = ErrUnavailable
						return
					}
					for _, user := range items {
						resp.Categories.User.Items = append(resp.Categories.User.Items, user)
					}
				}
			}
		})
	}
	if resp.Categories.Tag != nil {
		do(func() {
			if provider == nil {
				tagErr = ErrUnavailable
				return
			}
			limit := boundedLimit(state.TagOffset, size)
			tagCandidates, tagTotal, tagErr = provider.SearchTagCandidates(ctx, []string{q}, state.TagOffset, limit)
			if tagErr == nil && (tagTotal < 0 || int64(len(tagCandidates)) > limit) {
				tagErr = ErrUnavailable
			}
			for _, v := range tagCandidates {
				if v == nil || v.ID.IsZero() {
					tagErr = ErrUnavailable
				}
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil || tagErr != nil {
		return nil, ErrUnavailable
	}
	// Current source failure is global, even when users or another category won.
	var raw []videorepo.SearchCandidate
	if videoErr == nil && videoPage != nil {
		raw = append(raw, videoPage.Candidates...)
	}
	tagRaw := make([]videorepo.SearchCandidate, 0, len(tagCandidates))
	for _, v := range tagCandidates {
		tagRaw = append(tagRaw, videorepo.SearchCandidate{ID: v.ID.Hex()})
	}
	allRaw := append(append([]videorepo.SearchCandidate(nil), raw...), tagRaw...)
	current, err := s.currentCandidates(ctx, allRaw)
	if err != nil {
		return nil, ErrUnavailable
	}
	var videoMore, tagMore bool
	if cat := resp.Categories.Video; cat != nil {
		if videoErr != nil {
			cat.Status = feedmodel.CategoryStatusUnavailable
		} else {
			var consumed int64
			cat.Items, consumed = visibleCandidates(raw, current, size)
			state.VideoOffset += consumed
			cat.Total = videoPage.Total
			videoMore = consumed < int64(len(raw)) || !videoPage.Exhausted
		}
	}
	tagPreview := make([]*videomodel.Video, 0)
	if cat := resp.Categories.Tag; cat != nil {
		var consumed int64
		tagPreview, consumed = visibleCandidates(tagRaw, current, size)
		state.TagOffset += consumed
		tagMore = consumed < int64(len(tagRaw)) || state.TagOffset < tagTotal
		if tagTotal > 0 {
			cat.Items = []*feedmodel.TagResult{{Tag: q, Count: tagTotal}}
			cat.Total = 1
		}
	}
	if cat := resp.Categories.User; cat != nil && userErr != nil {
		resp.Categories.User = &feedmodel.UserCategory{Status: feedmodel.CategoryStatusUnavailable, Items: make([]interface{}, 0)}
	}
	succeeded := 0
	for _, category := range []struct {
		requested bool
		err       error
	}{{resp.Categories.Video != nil, videoErr}, {resp.Categories.User != nil, userErr}, {resp.Categories.Tag != nil, tagErr}} {
		if category.requested {
			if category.err == nil {
				succeeded++
			} else {
				resp.Partial = true
			}
		}
	}
	if succeeded == 0 || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	more := videoMore || tagMore
	boundary := page >= 100 || state.VideoOffset >= 5000 || state.TagOffset >= 5000
	resp.ScanLimitReached = boundary
	resp.HasMore = more && !boundary
	if resp.HasMore {
		resp.NextCursor = encodeCandidateCursor(q, typ, page+1, size, state.VideoOffset, state.TagOffset, s.candidateIndex())
	}
	if cat := resp.Categories.Video; cat != nil {
		cat.HasMore = videoMore && !boundary
	}
	if cat := resp.Categories.Video; cat != nil && videoErr == nil {
		resp.Videos, resp.Total = cat.Items, cat.Total
		resp.TotalRelation = "estimate"
	} else if resp.Categories.Tag != nil {
		resp.Videos, resp.Total = tagPreview, tagTotal
		resp.TotalRelation = "estimate"
	}
	if cat := resp.Categories.User; cat != nil {
		resp.Users = cat.Items
	}
	if cat := resp.Categories.Tag; cat != nil {
		resp.Tags = cat.Items
	}
	var envelope *candidateEnvelope
	if !resp.Partial {
		// The identity describes the window's starting offsets, not its output cursor.
		start, _ := decodeCandidateCursor(q, typ, page, size, cursor, s.candidateIndex())
		value := s.newCandidateEnvelope(q, typ, page, size, start)
		value.Video = videoPage
		value.User = resp.Categories.User
		envelope = &value
	}
	s.submitBackground(ctx, q, key, envelope)
	return resp, nil
}

func (s *SearchService) currentCandidates(ctx context.Context, candidates []videorepo.SearchCandidate) (map[string]*videomodel.Video, error) {
	if len(candidates) == 0 {
		return map[string]*videomodel.Video{}, nil
	}
	reader, ok := s.videoRepo.(videorepo.CurrentVideoReader)
	if !ok || reader == nil {
		return nil, ErrUnavailable
	}
	ids := make([]string, 0, len(candidates))
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		id, err := primitive.ObjectIDFromHex(candidate.ID)
		if err != nil {
			continue
		}
		canonical := id.Hex()
		if !seen[canonical] {
			seen[canonical] = true
			ids = append(ids, canonical)
		}
	}
	if len(ids) == 0 {
		return map[string]*videomodel.Video{}, nil
	}
	if len(ids) > 200 {
		return nil, ErrUnavailable
	}
	current, err := reader.BatchGetCurrentVideos(ctx, ids)
	if err != nil || current == nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	return current, nil
}

// Count every provider position while retaining source order and deduplicating
// only emitted videos. The unread suffix stays available to the next cursor.
func visibleCandidates(candidates []videorepo.SearchCandidate, current map[string]*videomodel.Video, size int64) ([]*videomodel.Video, int64) {
	videos := make([]*videomodel.Video, 0, size)
	seen := make(map[string]bool)
	var consumed int64
	for _, candidate := range candidates {
		consumed++
		id, err := primitive.ObjectIDFromHex(candidate.ID)
		if err != nil {
			continue
		}
		canonical := id.Hex()
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		v := current[canonical]
		if !videomodel.IsPubliclyVisible(v) || v.ID != id {
			continue
		}
		videos = append(videos, v)
		if int64(len(videos)) == size {
			break
		}
	}
	return videos, consumed
}
