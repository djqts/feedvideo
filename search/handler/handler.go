package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	feedmodel "feedvideo.local/search/model"
	feedrepo "feedvideo.local/search/repo"
	searchservice "feedvideo.local/search/service"

	"github.com/gin-gonic/gin"
)

// searchService is the interface Handler needs from the search service layer.
type searchService interface {
	Search(ctx context.Context, q string, typ string, page, size int64) (*feedmodel.SearchResp, error)
	HotWords(ctx context.Context, n int) ([]*feedmodel.HotWord, error)
	Suggest(ctx context.Context, prefix string, n int) ([]*feedmodel.SuggestItem, error)
}

type cursorSearchService interface {
	SearchWithCursor(ctx context.Context, q, typ string, page, size int64, cursor string) (*feedmodel.SearchResp, error)
}

type Handler struct {
	svc searchService
}

func NewHandler(svc searchService) *Handler { return &Handler{svc: svc} }

// Search godoc
// @Summary 综合搜索
// @Description 按 type 搜索视频 / 用户 / 标签，返回 schema_version=3 与分类状态；部分分类不可用时返回 200 且 partial=true
// @Tags 搜索
// @Produce json
// @Param q query string false "搜索关键词，去除首尾空白后最多 50 个 Unicode 字符；空关键词返回空分类"
// @Param type query string false "video/user/tag/all" default(all)
// @Param page query int false "页码，范围 1..100" default(1)
// @Param size query int false "每页数量，范围 1..50" default(10)
// @Param candidate_cursor query string false "续页候选位置，必须与 q/type/page/size 和当前搜索 index 一致"
// @Success 200 {object} map[string]interface{} "搜索结果"
// @Failure 400 {object} map[string]interface{} "参数非法、显式空值或重复；q 允许空值"
// @Failure 503 {object} map[string]interface{} "全部请求分类不可用"
// @Router /api/v1/search [get]
func (h *Handler) Search(c *gin.Context) {
	req, err := parseSearchQuery(c.Request.URL.RawQuery)
	if err != nil {
		writeSearchInvalidQuery(c, err)
		return
	}
	var resp *feedmodel.SearchResp
	ctx := c.Request.Context()
	if req.CandidateCursor != "" {
		cursorSvc, ok := h.svc.(cursorSearchService)
		if !ok {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "搜索暂时不可用，请稍后重试"})
			return
		}
		if req.Q != "" {
			ctx = samplingRequestContext(c)
		}
		resp, err = cursorSvc.SearchWithCursor(ctx, req.Q, string(req.Type), req.Page, req.Size, req.CandidateCursor)
	} else {
		if req.Q != "" {
			ctx = samplingRequestContext(c)
		}
		resp, err = h.svc.Search(ctx, req.Q, string(req.Type), req.Page, req.Size)
	}
	if err != nil {
		if errors.Is(err, feedmodel.ErrInvalidQuery) {
			writeSearchInvalidQuery(c, err)
			return
		}
		if errors.Is(err, searchservice.ErrUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "搜索暂时不可用，请稍后重试"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": resp})
}

const samplingSessionCookie = "fv_search_session"

type samplingHTTPSKey struct{}

func samplingRequestContext(c *gin.Context) context.Context {
	ctx := c.Request.Context()
	if value, exists := c.Get("userID"); exists {
		if id, ok := value.(string); ok && id != "" {
			return searchservice.WithSamplingActor(ctx, "account", id)
		}
	}
	id, _ := c.Cookie(samplingSessionCookie)
	valid := len(id) == 32
	for _, ch := range id {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			valid = false
		}
	}
	if !valid {
		var entropy [16]byte
		if _, err := io.ReadFull(rand.Reader, entropy[:]); err != nil {
			return searchservice.WithSamplingActor(ctx, "", "")
		}
		id = hex.EncodeToString(entropy[:])
		trustedHTTPS, _ := ctx.Value(samplingHTTPSKey{}).(bool)
		http.SetCookie(c.Writer, &http.Cookie{Name: samplingSessionCookie, Value: id, Path: "/api/v1/search", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: c.Request.TLS != nil || trustedHTTPS})
	}
	return searchservice.WithSamplingActor(ctx, "session", id)
}

// SamplingHTTPSMiddleware trusts only explicitly configured immediate peers.
// It does not infer authentication from any request header or cookie.
func SamplingHTTPSMiddleware(trustedProxies []string) gin.HandlerFunc {
	var networks []*net.IPNet
	for _, proxy := range trustedProxies {
		if ip := net.ParseIP(proxy); ip != nil {
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		} else if _, network, err := net.ParseCIDR(proxy); err == nil {
			networks = append(networks, network)
		}
	}
	return func(c *gin.Context) {
		secure := c.Request.TLS != nil
		if !secure {
			host, port, err := net.SplitHostPort(c.Request.RemoteAddr)
			portNumber, portErr := strconv.Atoi(port)
			ip := net.ParseIP(host)
			if err == nil && portErr == nil && portNumber >= 1 && portNumber <= 65535 && ip != nil {
				values := c.Request.Header.Values("X-Forwarded-Proto")
				if len(values) == 1 && values[0] == "https" {
					for _, network := range networks {
						if network.Contains(ip) {
							secure = true
							break
						}
					}
				}
			}
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), samplingHTTPSKey{}, secure))
		c.Next()
	}
}

func parseSearchQuery(rawQuery string) (feedmodel.SearchReq, error) {
	var req feedmodel.SearchReq
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return req, &feedmodel.QueryValidationError{Parameter: "query", Reason: "must use valid URL query encoding"}
	}
	q, err := searchQueryValue(values, "q", "", true)
	if err != nil {
		return req, err
	}
	typ, err := searchQueryValue(values, "type", "all", false)
	if err != nil {
		return req, err
	}
	pageRaw, err := searchQueryValue(values, "page", "1", false)
	if err != nil {
		return req, err
	}
	sizeRaw, err := searchQueryValue(values, "size", "10", false)
	if err != nil {
		return req, err
	}
	page, err := strconv.ParseInt(pageRaw, 10, 64)
	if err != nil {
		return req, &feedmodel.QueryValidationError{Parameter: "page", Reason: "must be an integer"}
	}
	size, err := strconv.ParseInt(sizeRaw, 10, 64)
	if err != nil {
		return req, &feedmodel.QueryValidationError{Parameter: "size", Reason: "must be an integer"}
	}
	q = strings.TrimSpace(q)
	if err := feedmodel.ValidateSearchRequest(q, typ, page, size); err != nil {
		return req, err
	}
	cursor, err := searchQueryValue(values, "candidate_cursor", "", false)
	if err != nil {
		return req, err
	}
	if cursor != "" {
		if err := searchservice.ValidateCandidateCursorSyntax(q, typ, page, size, cursor); err != nil {
			return req, err
		}
	}
	return feedmodel.SearchReq{Q: q, Type: feedmodel.SearchType(typ), Page: page, Size: size, CandidateCursor: cursor}, nil
}

func searchQueryValue(values url.Values, parameter, fallback string, allowEmpty bool) (string, error) {
	items, exists := values[parameter]
	if !exists {
		return fallback, nil
	}
	if len(items) != 1 {
		return "", &feedmodel.QueryValidationError{Parameter: parameter, Reason: "must appear exactly once"}
	}
	if items[0] == "" && !allowEmpty {
		return "", &feedmodel.QueryValidationError{Parameter: parameter, Reason: "must not be empty"}
	}
	return items[0], nil
}

func writeSearchInvalidQuery(c *gin.Context, err error) {
	body := gin.H{"code": 400, "message": "invalid query parameter"}
	var validation *feedmodel.QueryValidationError
	if errors.As(err, &validation) {
		body["details"] = gin.H{"parameter": validation.Parameter, "reason": validation.Reason}
	}
	c.JSON(http.StatusBadRequest, body)
}

// HotWords godoc
// @Summary 热搜词
// @Tags 搜索
// @Produce json
// @Param top_n query int false "数量" default(20)
// @Success 200 {object} map[string]interface{} "热搜词列表"
// @Router /api/v1/search/hot [get]
func (h *Handler) HotWords(c *gin.Context) {
	topNRaw := c.DefaultQuery("top_n", "20")
	n, err := strconv.Atoi(topNRaw)
	if err != nil || n < 1 || n > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "top_n", "reason": "must be an integer between 1 and 100"}})
		return
	}
	words, svcErr := h.svc.HotWords(c.Request.Context(), n)
	if svcErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": svcErr.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": words})
}

// Suggest godoc
// @Summary 搜索联想
// @Tags 搜索
// @Produce json
// @Param prefix query string true "前缀"
// @Param top_n query int false "数量" default(10)
// @Success 200 {object} map[string]interface{} "联想词列表"
// @Router /api/v1/search/suggest [get]
func (h *Handler) Suggest(c *gin.Context) {
	prefix := strings.TrimSpace(c.Query("prefix"))
	if prefix == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "prefix", "reason": "required"}})
		return
	}
	topNRaw := c.DefaultQuery("top_n", "10")
	n, err := strconv.Atoi(topNRaw)
	if err != nil || n < 1 || n > 50 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid query parameter", "details": gin.H{"parameter": "top_n", "reason": "must be an integer between 1 and 50"}})
		return
	}
	items, svcErr := h.svc.Suggest(c.Request.Context(), feedrepo.Normalize(prefix), n)
	if svcErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": svcErr.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": items})
}
