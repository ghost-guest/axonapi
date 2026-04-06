package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/httpclient"
)

type PublicBenefitHandlersParams struct {
	fx.In

	SystemService        *biz.SystemService
	PublicBenefitRuntime *biz.PublicBenefitRuntimeService
	HttpClient           *httpclient.HttpClient
}

func NewPublicBenefitHandlers(params PublicBenefitHandlersParams) *PublicBenefitHandlers {
	return &PublicBenefitHandlers{
		SystemService:        params.SystemService,
		PublicBenefitRuntime: params.PublicBenefitRuntime,
		httpClient:           params.HttpClient,
	}
}

type PublicBenefitHandlers struct {
	SystemService        *biz.SystemService
	PublicBenefitRuntime *biz.PublicBenefitRuntimeService
	httpClient           *httpclient.HttpClient
}

type ParseProviderIdentityRequest struct {
	Template string `json:"template"`
	BaseURL  string `json:"base_url"`
	Cookie   string `json:"cookie"`
	APIKey   string `json:"api_key"`
}

func (h *PublicBenefitHandlers) GetConfig(c *gin.Context) {
	cfg, err := h.SystemService.PublicBenefitHubConfig(c.Request.Context())
	if err != nil {
		JSONError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, cfg)
}

func (h *PublicBenefitHandlers) UpdateConfig(c *gin.Context) {
	var req objects.PublicBenefitHubConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		JSONError(c, http.StatusBadRequest, errors.New("invalid public benefit config payload"))
		return
	}

	if err := h.SystemService.SetPublicBenefitHubConfig(c.Request.Context(), req); err != nil {
		JSONError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *PublicBenefitHandlers) GetRuntime(c *gin.Context) {
	state, err := h.SystemService.PublicBenefitRuntimeState(c.Request.Context())
	if err != nil {
		JSONError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, state)
}

func (h *PublicBenefitHandlers) UpdateRuntime(c *gin.Context) {
	var req objects.PublicBenefitRuntimeState
	if err := c.ShouldBindJSON(&req); err != nil {
		JSONError(c, http.StatusBadRequest, errors.New("invalid public benefit runtime payload"))
		return
	}

	if err := h.SystemService.SetPublicBenefitRuntimeState(c.Request.Context(), req); err != nil {
		JSONError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *PublicBenefitHandlers) GetDashboard(c *gin.Context) {
	dashboard, err := h.SystemService.PublicBenefitDashboard(c.Request.Context())
	if err != nil {
		JSONError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, dashboard)
}

func (h *PublicBenefitHandlers) TriggerSync(c *gin.Context) {
	if err := h.PublicBenefitRuntime.RunSync(c.Request.Context()); err != nil {
		JSONError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *PublicBenefitHandlers) RefreshProvider(c *gin.Context) {
	providerID := c.Param("provider_id")
	if providerID == "" {
		JSONError(c, http.StatusBadRequest, errors.New("provider id is required"))
		return
	}

	if err := h.PublicBenefitRuntime.RefreshProvider(c.Request.Context(), providerID); err != nil {
		JSONError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *PublicBenefitHandlers) CheckInProvider(c *gin.Context) {
	providerID := c.Param("provider_id")
	if providerID == "" {
		JSONError(c, http.StatusBadRequest, errors.New("provider id is required"))
		return
	}

	if err := h.PublicBenefitRuntime.CheckInProvider(c.Request.Context(), providerID); err != nil {
		JSONError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *PublicBenefitHandlers) RefreshUpstream(c *gin.Context) {
	upstreamID := c.Param("upstream_id")
	if upstreamID == "" {
		JSONError(c, http.StatusBadRequest, errors.New("upstream id is required"))
		return
	}

	if err := h.PublicBenefitRuntime.RefreshUpstream(c.Request.Context(), upstreamID); err != nil {
		JSONError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *PublicBenefitHandlers) ParseProviderIdentity(c *gin.Context) {
	var req ParseProviderIdentityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		JSONError(c, http.StatusBadRequest, errors.New("invalid parse provider identity payload"))
		return
	}

	if req.Template != "new_api" {
		c.JSON(http.StatusOK, gin.H{"user_id": ""})
		return
	}

	if userID, ok := biz.ParseNewAPIUserIDFromCookie(req.Cookie); ok {
		c.JSON(http.StatusOK, gin.H{"user_id": userID, "source": "cookie"})
		return
	}

	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if baseURL == "" || strings.TrimSpace(req.Cookie) == "" {
		c.JSON(http.StatusOK, gin.H{"user_id": ""})
		return
	}

	provider := objects.PublicBenefitProviderAccount{
		BaseURL:  baseURL,
		Cookie:   strings.TrimSpace(req.Cookie),
		APIKey:   strings.TrimSpace(req.APIKey),
		Kind:     objects.PublicBenefitProviderKindNewAPI,
		AuthType: objects.PublicBenefitAuthTypeMixed,
	}

	httpReq := &httpclient.Request{
		Method:  http.MethodGet,
		URL:     provider.BaseURL + "/api/user/self",
		Headers: make(http.Header),
	}
	biz.ApplyPublicBenefitAuth(httpReq.Headers, provider)

	resp, err := h.httpClient.Do(c.Request.Context(), httpReq)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"user_id": ""})
		return
	}

	var payload struct {
		Data struct {
			ID       any `json:"id"`
			UserID   any `json:"user_id"`
			Username any `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		c.JSON(http.StatusOK, gin.H{"user_id": ""})
		return
	}

	for _, candidate := range []any{payload.Data.ID, payload.Data.UserID} {
		switch value := candidate.(type) {
		case string:
			if strings.TrimSpace(value) != "" {
				c.JSON(http.StatusOK, gin.H{"user_id": strings.TrimSpace(value), "source": "remote"})
				return
			}
		case float64:
			c.JSON(http.StatusOK, gin.H{"user_id": strconv.FormatInt(int64(value), 10), "source": "remote"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"user_id": ""})
}
