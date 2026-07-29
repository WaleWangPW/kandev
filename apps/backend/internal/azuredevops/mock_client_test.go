package azuredevops

import (
	"context"
	"testing"
)

func TestMockClientSeedsWorkspaceReadState(t *testing.T) {
	mock := NewMockClient()
	mock.Seed(MockState{
		Authenticated: true,
		User:          TestConnectionResult{OK: true, ID: "me", DisplayName: "Ada"},
		Projects:      []Project{{ID: "p1", Name: "Platform"}},
		WorkItems:     []WorkItem{{ID: 101, Title: "Fix build"}},
		PullRequests:  []PullRequest{{ID: 42, ProjectID: "p1", RepositoryID: "r1", Title: "Ship it"}},
		Feedback:      map[int]PullRequestFeedback{42: {ReviewState: "approved"}},
	})

	auth, err := mock.TestAuth(context.Background())
	if err != nil || !auth.OK || auth.ID != "me" {
		t.Fatalf("auth = %+v, %v", auth, err)
	}
	projects, _ := mock.ListProjects(context.Background())
	work, _ := mock.QueryWIQL(context.Background(), "p1", "SELECT", 20)
	prs, _ := mock.ListPullRequests(context.Background(), PullRequestFilter{ProjectID: "p1", RepositoryID: "r1"})
	feedback, ok := mock.Feedback(42)
	if len(projects) != 1 || len(work.Items) != 1 || len(prs.Items) != 1 || !ok || feedback.ReviewState != "approved" {
		t.Fatalf("mock state projects=%+v work=%+v prs=%+v feedback=%+v", projects, work, prs, feedback)
	}
}

func TestMockClientProvidesWorkItemDetailDiscussionAndIdentity(t *testing.T) {
	mock := NewMockClient()
	mock.Seed(MockState{
		Authenticated: true,
		User:          TestConnectionResult{OK: true, ID: "me", DisplayName: "Ada", Email: "ada@example.com"},
		WorkItems: []WorkItem{{
			ID: 101, Description: "<p>Useful</p><script>alert('xss')</script>",
			Fields: map[string]any{"Microsoft.VSTS.Scheduling.Effort": 5},
		}},
		WorkItemComments: map[int][]WorkItemComment{101: {{ID: 2, Content: "Newest"}, {ID: 1, Content: "Older"}}},
	})

	detail, err := mock.GetWorkItemDetail(t.Context(), "p1", 101)
	if err != nil || detail.Description != "Useful" || len(detail.PlanningFields) != 1 {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	comments, err := mock.ListWorkItemComments(t.Context(), "p1", 101, "opaque")
	if err != nil || len(comments.Comments) != 2 || comments.Comments[0].ID != 2 {
		t.Fatalf("comments = %+v, %v", comments, err)
	}
	identity, err := mock.GetCurrentIdentity(t.Context())
	if err != nil || identity.ID != "me" || identity.UniqueName != "ada@example.com" {
		t.Fatalf("identity = %+v, %v", identity, err)
	}
}

func TestMockClientPaginatesPullRequests(t *testing.T) {
	mock := NewMockClient()
	mock.Seed(MockState{PullRequests: []PullRequest{
		{ID: 1, ProjectID: "p1", Status: activePullRequestState},
		{ID: 2, ProjectID: "p1", Status: activePullRequestState},
	}})

	page, err := mock.ListPullRequests(t.Context(), PullRequestFilter{
		ProjectID: "p1", Status: activePullRequestState, Skip: 1, Top: 1,
	})
	if err != nil {
		t.Fatalf("list pull requests: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != 2 {
		t.Fatalf("items = %#v, want second pull request only", page.Items)
	}
}

func TestMockClientBoardReads(t *testing.T) {
	mock := NewMockClient()
	mock.Seed(MockState{
		Teams:          []Team{{ID: "team-1", Name: "Platform Team", ProjectID: "p1"}},
		Boards:         []BoardReference{{ID: "board-1", Name: "Stories"}},
		BoardSnapshots: map[string]BoardSnapshot{"board-1": {Board: Board{ID: "board-1"}, Items: []BoardWorkItem{{WorkItem: WorkItem{ID: 101}}}}},
	})
	teams, err := mock.ListTeams(t.Context(), "p1")
	if err != nil || len(teams) != 1 {
		t.Fatalf("teams = %+v, %v", teams, err)
	}
	boards, err := mock.ListBoards(t.Context(), "p1", "team-1")
	if err != nil || len(boards) != 1 || boards[0].ID != "board-1" {
		t.Fatalf("boards = %+v, %v", boards, err)
	}
	snapshot, err := mock.GetBoardSnapshot(t.Context(), "p1", "team-1", "board-1")
	if err != nil || snapshot == nil || len(snapshot.Items) != 1 {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
	snapshot.Items[0].Title = "Mutated caller copy"
	again, err := mock.GetBoardSnapshot(t.Context(), "p1", "team-1", "board-1")
	if err != nil || again.Items[0].Title == "Mutated caller copy" {
		t.Fatalf("subsequent snapshot = %+v, %v", again, err)
	}
}

func TestMockClientUpdateBoardWorkItem(t *testing.T) {
	mock := NewMockClient()
	action := "assign_current_user"
	mock.Seed(MockState{
		Authenticated:  true,
		User:           TestConnectionResult{OK: true, ID: "me", DisplayName: "Ada"},
		BoardSnapshots: map[string]BoardSnapshot{"board-1": {Board: Board{ID: "board-1"}, Items: []BoardWorkItem{{WorkItem: WorkItem{ID: 101, Revision: 7, Title: "Original"}}}}},
	})
	item, err := mock.UpdateBoardWorkItem(t.Context(), "p1", "team-1", "board-1", 101, BoardWorkItemUpdateRequest{Revision: 7, AssigneeAction: &action})
	if err != nil || item == nil || item.AssignedTo != "Ada" || item.Revision != 8 {
		t.Fatalf("updated item = %+v, %v", item, err)
	}
}

func TestMockClientUpdateBoardWorkItemInitializesFieldsForDoneOnlyUpdate(t *testing.T) {
	mock := NewMockClient()
	done := true
	mock.Seed(MockState{
		BoardSnapshots: map[string]BoardSnapshot{"board-1": {
			Board: Board{ID: "board-1", Fields: BoardFields{DoneField: FieldReference{ReferenceName: "System.BoardDone"}}},
			Items: []BoardWorkItem{{WorkItem: WorkItem{ID: 101, Revision: 7}}},
		}},
	})

	item, err := mock.UpdateBoardWorkItem(t.Context(), "p1", "team-1", "board-1", 101, BoardWorkItemUpdateRequest{Revision: 7, ColumnDone: &done})
	if err != nil || item == nil || !item.ColumnDone || item.Fields["System.BoardDone"] != true {
		t.Fatalf("updated item = %+v, %v", item, err)
	}
}

func TestMockClientBoardUpdateKeepsWorkItemReadsInSync(t *testing.T) {
	mock := NewMockClient()
	action := "assign_current_user"
	mock.Seed(MockState{
		Authenticated: true,
		User:          TestConnectionResult{OK: true, ID: "me", DisplayName: "Ada"},
		BoardSnapshots: map[string]BoardSnapshot{"board-1": {
			Board: Board{ID: "board-1"},
			Items: []BoardWorkItem{{WorkItem: WorkItem{ID: 101, Revision: 7, Title: "Original"}}},
		}},
		WorkItems: []WorkItem{{ID: 101, Revision: 7, Title: "Original"}},
	})

	if _, err := mock.UpdateBoardWorkItem(t.Context(), "p1", "team-1", "board-1", 101, BoardWorkItemUpdateRequest{Revision: 7, AssigneeAction: &action}); err != nil {
		t.Fatalf("update board item: %v", err)
	}
	item, err := mock.GetWorkItem(t.Context(), "p1", 101)
	if err != nil || item.AssignedTo != "Ada" || item.Revision != 8 {
		t.Fatalf("work item = %+v, %v", item, err)
	}
}
