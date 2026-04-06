package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/apikey"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/project"
	"github.com/looplj/axonhub/internal/ent/user"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/internal/server/biz"
)

func TestWithAPIKeyConfig_RejectsNoAuthKeyWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(WithAPIKeyConfig(&biz.AuthService{}, nil))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+biz.NoAuthAPIKeyValue)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, recorder.Code)
	}
}

func TestWithAPIKeyConfig_AllowsMissingAuthorizationWhenNoAuthAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		key, err := ExtractAPIKeyFromRequest(c.Request, &APIKeyConfig{
			Headers:       []string{"Authorization"},
			RequireBearer: true,
		})
		if errors.Is(err, ErrAPIKeyRequired) {
			c.Status(http.StatusNoContent)
			c.Abort()

			return
		}

		if err != nil || key != "" {
			c.Status(http.StatusTeapot)
			c.Abort()

			return
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, recorder.Code)
	}
}

func TestWithAPIKeyConfig_AllowsPublicBenefitUnifiedAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))

	_, err := client.System.Create().SetKey(biz.SystemKeySecretKey).SetValue("test-secret").Save(ctx)
	require.NoError(t, err)

	hashedPassword, err := biz.HashPassword("test-password")
	require.NoError(t, err)

	_, err = client.User.Create().
		SetEmail("owner-middleware@example.com").
		SetPassword(hashedPassword).
		SetFirstName("Owner").
		SetLastName("User").
		SetIsOwner(true).
		SetStatus(user.StatusActivated).
		Save(ctx)
	require.NoError(t, err)

	defaultProject, err := client.Project.Create().
		SetName(uuid.NewString()).
		SetDescription("default").
		SetStatus(project.StatusActive).
		SetCreatedAt(time.Now()).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	systemService := &biz.SystemService{
		AbstractService: &biz.AbstractService{},
		Cache:           xcache.NewFromConfig[ent.System](xcache.Config{}),
	}

	projectService := &biz.ProjectService{
		AbstractService: &biz.AbstractService{},
		ProjectCache:    xcache.NewFromConfig[ent.Project](xcache.Config{}),
	}

	apiKeyService := biz.NewAPIKeyService(biz.APIKeyServiceParams{
		CacheConfig:    xcache.Config{},
		Ent:            client,
		ProjectService: projectService,
	})
	defer apiKeyService.Stop()

	authService := &biz.AuthService{
		AbstractService: &biz.AbstractService{},
		SystemService:   systemService,
		APIKeyService:   apiKeyService,
		AllowNoAuth:     false,
	}

	err = systemService.SetPublicBenefitHubConfig(ctx, objects.PublicBenefitHubConfig{
		Outbound: objects.PublicBenefitOutboundConfig{
			Enabled:      true,
			PublicAPIKey: "pb-middleware-key",
		},
	})
	require.NoError(t, err)

	router := gin.New()
	router.Use(WithAPIKeyConfig(authService, nil))
	router.GET("/test", func(c *gin.Context) {
		apiKey, ok := contexts.GetAPIKey(c.Request.Context())
		require.True(t, ok)
		require.NotNil(t, apiKey)
		require.Equal(t, apikey.TypeNoauth, apiKey.Type)
		require.Equal(t, biz.NoAuthAPIKeyValue, apiKey.Key)

		projectID, ok := contexts.GetProjectID(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, defaultProject.ID, projectID)

		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req = req.WithContext(ent.NewContext(req.Context(), client))
	req.Header.Set("Authorization", "Bearer pb-middleware-key")

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
}
