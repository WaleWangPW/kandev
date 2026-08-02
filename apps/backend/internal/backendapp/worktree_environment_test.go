package backendapp

import (
	"context"
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

type stubExecutorProfileGetter struct {
	profile *models.ExecutorProfile
	err     error
}

func (s *stubExecutorProfileGetter) GetExecutorProfile(context.Context, string) (*models.ExecutorProfile, error) {
	return s.profile, s.err
}

func TestEnvironmentDestroyerAdapterDockerHostForEnvironment(t *testing.T) {
	t.Run("uses executor profile docker host", func(t *testing.T) {
		adapter := &environmentDestroyerAdapter{profiles: &stubExecutorProfileGetter{
			profile: &models.ExecutorProfile{Config: map[string]string{
				"docker_host": " unix:///tmp/kandev-docker.sock ",
			}},
		}}

		got, err := adapter.dockerHostForEnvironment(context.Background(), &models.TaskEnvironment{
			ExecutorProfileID: "profile-1",
		})
		if err != nil {
			t.Fatalf("dockerHostForEnvironment: %v", err)
		}
		if got != "unix:///tmp/kandev-docker.sock" {
			t.Fatalf("docker host = %q, want profile host", got)
		}
	})

	t.Run("missing profile fails closed", func(t *testing.T) {
		adapter := &environmentDestroyerAdapter{profiles: &stubExecutorProfileGetter{
			err: errors.New("profile unavailable"),
		}}

		if _, err := adapter.dockerHostForEnvironment(context.Background(), &models.TaskEnvironment{
			ExecutorProfileID: "profile-1",
		}); err == nil {
			t.Fatal("expected profile lookup error")
		}
	})

	t.Run("legacy environment uses default host", func(t *testing.T) {
		adapter := &environmentDestroyerAdapter{}
		got, err := adapter.dockerHostForEnvironment(context.Background(), &models.TaskEnvironment{})
		if err != nil {
			t.Fatalf("dockerHostForEnvironment: %v", err)
		}
		if got != "" {
			t.Fatalf("docker host = %q, want default", got)
		}
	})
}
