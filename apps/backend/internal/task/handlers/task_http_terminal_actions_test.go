package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/kandev/kandev/internal/task/service"
)

func TestHTTPTerminalActionRoutesRespectFlags(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskSvc := service.NewService(service.Repos{}, nil, newTestLogger(t), service.RepositoryDiscoveryConfig{})
	handler := &TaskHandlers{service: taskSvc, logger: newTestLogger(t)}
	router := gin.New()
	registerTerminalActionRoutes(router, handler, false, false)

	for _, path := range []string{
		"/api/v1/tasks/task/resource-cleanup/cancel-archive",
		"/api/v1/system/resource-cleanup/cancel-stale-pending-move",
		"/api/v1/system/resource-cleanup/release-absent-retained-target",
		"/api/v1/system/resource-cleanup/retire-stale-environment-reference",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{}`))
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("disabled %s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestHTTPCancelStalePendingMoveRejectsInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskSvc := service.NewService(service.Repos{}, nil, newTestLogger(t), service.RepositoryDiscoveryConfig{})
	handler := &TaskHandlers{service: taskSvc, logger: newTestLogger(t)}
	router := gin.New()
	registerTerminalActionRoutes(router, handler, true, false)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/resource-cleanup/cancel-stale-pending-move",
		bytes.NewBufferString("{not-json"))
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want 400", recorder.Code)
	}
}

func TestHTTPReleaseAbsentRejectsUnknownFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskSvc := service.NewService(service.Repos{}, nil, newTestLogger(t), service.RepositoryDiscoveryConfig{})
	taskSvc.SetArchivedResourceFeatures(true, true)
	handler := &TaskHandlers{service: taskSvc, logger: newTestLogger(t)}
	router := gin.New()
	registerTerminalActionRoutes(router, handler, true, true)
	body := bytes.NewBufferString(`{"anchor_operation_id":"op","unknown":"x"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/resource-cleanup/release-absent-retained-target", body)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d, want 400 body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHTTPRetireStaleEnvironmentRejectsInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskSvc := service.NewService(service.Repos{}, nil, newTestLogger(t), service.RepositoryDiscoveryConfig{})
	taskSvc.SetArchivedResourceFeatures(true, false)
	handler := &TaskHandlers{service: taskSvc, logger: newTestLogger(t)}
	router := gin.New()
	registerTerminalActionRoutes(router, handler, true, false)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/resource-cleanup/retire-stale-environment-reference",
		bytes.NewBufferString("{not-json"))
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want 400", recorder.Code)
	}
}

func registerTerminalActionRoutes(router *gin.Engine, handler *TaskHandlers, reconcileEnabled, physicalReleaseEnabled bool) {
	api := router.Group("/api/v1")
	if reconcileEnabled {
		api.POST("/system/resource-cleanup/cancel-stale-pending-move", handler.httpCancelStaleArchivedResourcePendingMove)
		api.POST("/system/resource-cleanup/retire-stale-environment-reference", handler.httpRetireStaleArchivedResourceEnvironmentReference)
	}
	if physicalReleaseEnabled {
		api.POST("/system/resource-cleanup/release-absent-retained-target", handler.httpReleaseAbsentArchivedResourceTarget)
	}
}
