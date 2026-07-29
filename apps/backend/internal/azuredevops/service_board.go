package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (s *Service) boardReaderForWorkspace(ctx context.Context, workspaceID string) (BoardReader, error) {
	client, err := s.clientForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	reader, ok := client.(BoardReader)
	if !ok {
		return nil, errors.New("azure devops: board listing is unavailable")
	}
	return reader, nil
}

func (s *Service) ListTeamsForWorkspace(ctx context.Context, workspaceID, projectID string) ([]Team, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: project required", ErrInvalidConfig)
	}
	reader, err := s.boardReaderForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return reader.ListTeams(ctx, projectID)
}

func (s *Service) ListBoardsForWorkspace(ctx context.Context, workspaceID, projectID, teamID string) ([]BoardReference, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(teamID) == "" {
		return nil, fmt.Errorf("%w: project and team required", ErrInvalidConfig)
	}
	reader, err := s.boardReaderForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return reader.ListBoards(ctx, projectID, teamID)
}

func (s *Service) GetBoardSnapshotForWorkspace(ctx context.Context, workspaceID, projectID, teamID, boardID string) (*BoardSnapshot, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(teamID) == "" || strings.TrimSpace(boardID) == "" {
		return nil, fmt.Errorf("%w: project, team, and board required", ErrInvalidConfig)
	}
	reader, err := s.boardReaderForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return reader.GetBoardSnapshot(ctx, projectID, teamID, boardID)
}

func (s *Service) UpdateBoardWorkItemForWorkspace(ctx context.Context, workspaceID, projectID, teamID, boardID string, id int, request BoardWorkItemUpdateRequest) (*BoardWorkItem, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(teamID) == "" || strings.TrimSpace(boardID) == "" || id <= 0 {
		return nil, fmt.Errorf("%w: project, team, board, and positive work item id required", ErrInvalidConfig)
	}
	if request.Revision <= 0 {
		return nil, fmt.Errorf("%w: revision required", ErrInvalidConfig)
	}
	client, err := s.clientForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	writer, ok := client.(BoardWriter)
	if !ok {
		return nil, errors.New("azure devops: board updates are unavailable")
	}
	return writer.UpdateBoardWorkItem(ctx, projectID, teamID, boardID, id, request)
}
