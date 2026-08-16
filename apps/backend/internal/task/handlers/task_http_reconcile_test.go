package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/kandev/kandev/internal/task/repository"
	"github.com/kandev/kandev/internal/task/service"
)

func TestHTTPReconcileArchivedResourceRoutesRequireFlag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskSvc := service.NewService(service.Repos{}, nil, newTestLogger(t), service.RepositoryDiscoveryConfig{})
	handler := &TaskHandlers{service: taskSvc, logger: newTestLogger(t)}
	router := gin.New()
	registerReconcileHandlerRoutes(router, handler, false)

	body := bytes.NewBufferString(`{"expected_archived_at":"2026-08-12T00:00:00.000000001Z","target":{"worktree_id":"wt-1","repository_id":"repo-1","repository_path":"/tmp/repo","git_common_dir":"/tmp/repo/.git","worktree_path":"/tmp/worktree","branch":"feature/synthetic","head_oid":"` + strings.Repeat("a", 40) + `","associations":[]}}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/resource-cleanup/reconcile", body)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled route served status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHTTPReconcileArchivedResourceRejectsInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskSvc := service.NewService(service.Repos{}, nil, newTestLogger(t), service.RepositoryDiscoveryConfig{})
	handler := &TaskHandlers{service: taskSvc, logger: newTestLogger(t)}
	router := gin.New()
	registerReconcileHandlerRoutes(router, handler, true)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/resource-cleanup/reconcile",
		bytes.NewBufferString("{not-json"))
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want 400", recorder.Code)
	}
}

func TestHTTPReconcileArchivedResourceRejectsDuplicateKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskSvc := service.NewService(service.Repos{}, nil, newTestLogger(t), service.RepositoryDiscoveryConfig{})
	handler := &TaskHandlers{service: taskSvc, logger: newTestLogger(t)}
	router := gin.New()
	registerReconcileHandlerRoutes(router, handler, true)
	recorder := httptest.NewRecorder()
	body := `{"expected_archived_at":"2026-08-12T00:00:00.000000001Z","expected_archived_at":"2026-08-12T00:00:00.000000002Z"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/resource-cleanup/reconcile",
		bytes.NewBufferString(body))
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("duplicate-key status = %d, want 400", recorder.Code)
	}
}

func TestHTTPReconcileArchivedResourceRejectsUnknownFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskSvc := service.NewService(service.Repos{}, nil, newTestLogger(t), service.RepositoryDiscoveryConfig{})
	handler := &TaskHandlers{service: taskSvc, logger: newTestLogger(t)}
	router := gin.New()
	registerReconcileHandlerRoutes(router, handler, true)
	recorder := httptest.NewRecorder()
	body := `{"expected_archived_at":"2026-08-12T00:00:00.000000001Z","extra_field":"x"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/resource-cleanup/reconcile",
		bytes.NewBufferString(body))
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d, want 400", recorder.Code)
	}
}

func TestHTTPReconcileArchivedResourceGroupRejectsInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskSvc := service.NewService(service.Repos{}, nil, newTestLogger(t), service.RepositoryDiscoveryConfig{})
	handler := &TaskHandlers{service: taskSvc, logger: newTestLogger(t)}
	router := gin.New()
	registerReconcileHandlerRoutes(router, handler, true)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/resource-cleanup/reconcile-group",
		bytes.NewBufferString("{not-json"))
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status = %d, want 400", recorder.Code)
	}
}

func TestHTTPReconcileArchivedResourceGroupRejectsInvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskSvc := service.NewService(service.Repos{}, nil, newTestLogger(t), service.RepositoryDiscoveryConfig{})
	taskSvc.SetArchivedResourceFeatures(true, false)
	handler := &TaskHandlers{service: taskSvc, logger: newTestLogger(t)}
	router := gin.New()
	registerReconcileHandlerRoutes(router, handler, true)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/resource-cleanup/reconcile-group",
		bytes.NewBufferString(`{}`))
	router.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusOK {
		t.Fatalf("empty group reconcile accepted")
	}
}

func TestArchivedResourceReconcileHTTPStatus(t *testing.T) {
	cases := map[error]int{
		repository.ErrTaskNotFound:                      http.StatusNotFound,
		service.ErrArchivedResourceReconcileInvalid:     http.StatusBadRequest,
		service.ErrArchivedResourceReconcileConflict:    http.StatusConflict,
		service.ErrArchivedResourceReconcileDisabled:    http.StatusNotFound,
		service.ErrArchivedResourceReconcileUnavailable: http.StatusServiceUnavailable,
		errors.New("unknown"):                           http.StatusInternalServerError,
	}
	for err, want := range cases {
		if got := archivedResourceReconcileHTTPStatus(err); got != want {
			t.Fatalf("status(%v) = %d, want %d", err, got, want)
		}
	}
}

func TestRejectDuplicateArchivedResourceJSONKeys(t *testing.T) {
	if err := rejectDuplicateArchivedResourceJSONKeys([]byte(`{"a":1,"a":2}`)); err == nil {
		t.Fatal("duplicate keys accepted")
	}
	if err := rejectDuplicateArchivedResourceJSONKeys([]byte(`{}`)); err != nil {
		t.Fatalf("empty object rejected: %v", err)
	}
	if err := rejectDuplicateArchivedResourceJSONKeys([]byte(`{"a":1} 1`)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if err := rejectDuplicateArchivedResourceJSONKeys([]byte(``)); err == nil {
		t.Fatal("empty body accepted")
	}
}

func TestWalkArchivedResourceJSONValueValidatesStructure(t *testing.T) {
	if err := rejectDuplicateArchivedResourceJSONKeys([]byte(`[1, 2]`)); err != nil {
		t.Fatalf("array of numbers rejected: %v", err)
	}
	if err := rejectDuplicateArchivedResourceJSONKeys([]byte(`{"a":[1,2,{"b":1}]}`)); err != nil {
		t.Fatalf("nested array rejected: %v", err)
	}
}

func registerReconcileHandlerRoutes(router *gin.Engine, handler *TaskHandlers, enabled bool) {
	api := router.Group("/api/v1")
	if enabled {
		api.POST("/tasks/:id/resource-cleanup/reconcile", handler.httpReconcileArchivedResource)
		api.POST("/system/resource-cleanup/reconcile-group", handler.httpReconcileArchivedResourceGroup)
	}
}
