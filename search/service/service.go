package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	feedmodel "feedvideo.local/search/model"

	sharedconfig "feedvideo.local/shared-config"
	usermodel "feedvideo.local/user/model"
	videomodel "feedvideo.local/video/model"
	videorepo "feedvideo.local/video/repo"
)

// searchRepo is the minimal repository dependency of SearchService.
// *repo.SearchRepo satisfies it.
type searchRepo interface {
	GetCache(ctx context.Context, key string) ([]byte, error)
	SetCache(ctx context.Context, key string, data []byte) error
	IncrHotWord(ctx context.Context, q string) error
	AddSuggest(ctx context.Context, q string) error
	TopHotWords(ctx context.Context, n int) ([]struct {
		Word  string
		Score float64
	}, error)
	GetSuggest(ctx context.Context, prefix string, n int) ([]struct {
		Word  string
		Score float64
	}, error)
}

// userSearcher is the minimal user search dependency. *userservice.SearchService satisfies it.
type userSearcher interface {
	SearchUsers(ctx context.Context, query string, page, size int) (*usermodel.SearchResponse, error)
}

type SearchService struct {
	videoRepo        videorepo.VideoRepo
	userSvc          userSearcher
	repo             searchRepo
	cfg              sharedconfig.SearchConfig
	cacheNamespace   string
	backgroundMu     sync.Mutex
	background       *backgroundRecorder
	backgroundSealed bool
}

// ConfigureCacheNamespace is called before requests start; legacy keeps the C1 cache identity.
func (s *SearchService) ConfigureCacheNamespace(index string) { s.cacheNamespace = index }

// ErrUnavailable means no requested category succeeded or current video facts
// could not be verified within the request deadline.
var ErrUnavailable = videorepo.ErrSearchUnavailable

func NewSearchService(videoRepo videorepo.VideoRepo, userSvc userSearcher, repo searchRepo, cfg sharedconfig.SearchConfig) *SearchService {
	if cfg.SuggestMax <= 0 {
		cfg.SuggestMax = 10
	}
	background, _ := newBackgroundRecorder(repo, BackgroundOptions{})
	return &SearchService{videoRepo: videoRepo, userSvc: userSvc, repo: repo, cfg: cfg, background: background}
}

// Configuration is sealed before a lifecycle transition. A failed option set
// preserves the current recorder; Start/Stop can never orphan its consumers.
func (s *SearchService) ConfigureBackground(opts BackgroundOptions) error {
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	if s.backgroundSealed {
		return errors.New("search background configuration is sealed")
	}
	recorder, err := newBackgroundRecorder(s.repo, opts)
	if err != nil {
		return err
	}
	s.background = recorder
	return nil
}

func (s *SearchService) StartBackground(ctx context.Context) error {
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	if s.backgroundSealed {
		return errors.New("search background service cannot be restarted")
	}
	s.backgroundSealed = true
	if s.repo == nil {
		return ctx.Err()
	}
	return s.background.Start(ctx)
}

func (s *SearchService) StopBackground(ctx context.Context) error {
	s.backgroundMu.Lock()
	s.backgroundSealed = true
	recorder := s.background
	s.backgroundMu.Unlock()
	if s.repo == nil {
		return nil
	}
	return recorder.Stop(ctx)
}

func (s *SearchService) WaitBackground(ctx context.Context) error {
	s.backgroundMu.Lock()
	recorder := s.background
	s.backgroundMu.Unlock()
	if s.repo == nil {
		return nil
	}
	return recorder.Wait(ctx)
}

func (s *SearchService) BackgroundStats() BackgroundStats {
	s.backgroundMu.Lock()
	recorder := s.background
	s.backgroundMu.Unlock()
	if s.repo == nil {
		return BackgroundStats{}
	}
	return recorder.Stats()
}

func (s *SearchService) SearchWithCursor(ctx context.Context, q, typ string, page, size int64, cursor string) (*feedmodel.SearchResp, error) {
	return s.searchCurrent(ctx, q, typ, page, size, cursor)
}

func (s *SearchService) Search(ctx context.Context, q string, typ string, page, size int64) (*feedmodel.SearchResp, error) {
	return s.SearchWithCursor(ctx, q, typ, page, size, "")
}

func emptySearchResponse(typ string) *feedmodel.SearchResp {
	resp := &feedmodel.SearchResp{
		SchemaVersion: feedmodel.SearchSchemaVersion,
		Categories:    &feedmodel.SearchCategories{},
		Videos:        make([]*videomodel.Video, 0),
		Users:         make([]interface{}, 0),
		Tags:          make([]*feedmodel.TagResult, 0),
	}
	if typ == "video" || typ == "all" {
		resp.Categories.Video = &feedmodel.VideoCategory{Status: feedmodel.CategoryStatusOK, Items: make([]*videomodel.Video, 0)}
	}
	if typ == "user" || typ == "all" {
		resp.Categories.User = &feedmodel.UserCategory{Status: feedmodel.CategoryStatusOK, Items: make([]interface{}, 0)}
	}
	if typ == "tag" || typ == "all" {
		resp.Categories.Tag = &feedmodel.TagCategory{Status: feedmodel.CategoryStatusOK, Items: make([]*feedmodel.TagResult, 0)}
	}
	return resp
}

// completeCategories rejects legacy/partial cache entries and mismatched sets.
func completeCategories(resp *feedmodel.SearchResp, typ string, raw []byte) bool {
	if resp.SchemaVersion != feedmodel.SearchSchemaVersion || resp.Partial || resp.Categories == nil {
		return false
	}
	// The typed decoder ignores unknown fields and maps null pointers to absent
	// categories, so check the actual JSON keys before trusting the typed shape.
	var wire struct {
		Categories map[string]json.RawMessage `json:"categories"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return false
	}
	want := 1
	if typ == "all" {
		want = 3
	}
	if len(wire.Categories) != want {
		return false
	}
	for name := range wire.Categories {
		if name != "video" && name != "user" && name != "tag" {
			return false
		}
		if typ != "all" && name != typ {
			return false
		}
	}
	c := resp.Categories
	video, user, tag := typ == "video" || typ == "all", typ == "user" || typ == "all", typ == "tag" || typ == "all"
	if (c.Video != nil) != video || (c.User != nil) != user || (c.Tag != nil) != tag {
		return false
	}
	return (c.Video == nil || (c.Video.Status == feedmodel.CategoryStatusOK && c.Video.Items != nil)) &&
		(c.User == nil || (c.User.Status == feedmodel.CategoryStatusOK && c.User.Items != nil)) &&
		(c.Tag == nil || (c.Tag.Status == feedmodel.CategoryStatusOK && c.Tag.Items != nil))
}

func (s *SearchService) submitBackground(ctx context.Context, q, key string, candidates *candidateEnvelope) {
	if s.repo == nil {
		return
	}
	var frozen []byte
	if candidates != nil {
		if data, err := json.Marshal(candidates); err == nil && len(data) <= maxCandidateCacheBytes {
			frozen = data
		}
	}
	actor := samplingActorFromContext(ctx)
	s.backgroundMu.Lock()
	recorder := s.background
	s.backgroundMu.Unlock()
	recorder.Submit(q, key, frozen, actor.kind, actor.id)
}

func (s *SearchService) HotWords(ctx context.Context, n int) ([]*feedmodel.HotWord, error) {
	if n <= 0 || n > s.cfg.HotMax {
		n = s.cfg.HotMax
	}
	zs, err := s.repo.TopHotWords(ctx, n)
	if err != nil {
		return nil, err
	}
	out := make([]*feedmodel.HotWord, 0, len(zs))
	for _, z := range zs {
		out = append(out, &feedmodel.HotWord{Word: z.Word, Score: z.Score})
	}
	return out, nil
}

func (s *SearchService) Suggest(ctx context.Context, prefix string, n int) ([]*feedmodel.SuggestItem, error) {
	if n <= 0 || n > s.cfg.SuggestMax {
		n = s.cfg.SuggestMax
	}
	zs, err := s.repo.GetSuggest(ctx, prefix, n)
	if err != nil {
		return nil, err
	}
	out := make([]*feedmodel.SuggestItem, 0, len(zs))
	for _, z := range zs {
		out = append(out, &feedmodel.SuggestItem{Word: z.Word, Score: z.Score})
	}
	return out, nil
}
