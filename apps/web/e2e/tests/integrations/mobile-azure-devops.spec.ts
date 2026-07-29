import { expect, test } from "../../fixtures/test-base";

const MOCK_STATE = {
  authenticated: true,
  user: { ok: true, id: "user-1", displayName: "Ada Reviewer" },
  projects: [{ id: "project-1", name: "Platform", url: "https://dev.azure.com/acme/Platform" }],
  teams: [{ id: "team-1", name: "Platform Team", projectId: "project-1", projectName: "Platform" }],
  boards: [{ id: "board-1", name: "Stories" }],
  boardSnapshots: {
    "board-1": {
      board: {
        id: "board-1",
        name: "Stories",
        fields: {
          columnField: { referenceName: "System.BoardColumn" },
          doneField: { referenceName: "System.BoardColumnDone" },
          rowField: { referenceName: "System.BoardRow" },
        },
        columns: [
          { id: "todo", name: "To Do" },
          { id: "active", name: "Active" },
          { id: "done", name: "Done" },
        ],
      },
      items: [
        {
          id: 101,
          revision: 3,
          title: "Handle token rotation",
          state: "Active",
          type: "User Story",
          project: "project-1",
          columnId: "todo",
          columnDone: false,
        },
      ],
    },
  },
  repositories: [
    {
      id: "azure-repo-1",
      name: "api",
      projectId: "project-1",
      projectName: "Platform",
      defaultBranch: "refs/heads/main",
      webUrl: "https://dev.azure.com/acme/Platform/_git/api",
    },
  ],
  workItems: [
    {
      id: 101,
      revision: 3,
      title: "Handle token rotation",
      state: "Active",
      type: "User Story",
      project: "project-1",
    },
  ],
  pullRequests: [
    {
      id: 42,
      title: "Rotate integration credentials",
      status: "active",
      isDraft: false,
      sourceBranch: "refs/heads/feature/rotation",
      targetBranch: "refs/heads/main",
      author: { id: "user-1", displayName: "Ada Reviewer" },
      projectId: "project-1",
      projectName: "Platform",
      repositoryId: "azure-repo-1",
      repositoryName: "api",
      webUrl: "https://dev.azure.com/acme/Platform/_git/api/pullrequest/42",
      apiUrl: "https://dev.azure.com/acme/Platform/_apis/git/pullrequests/42",
    },
  ],
  feedback: {
    "42": {
      pullRequest: {
        id: 42,
        title: "Rotate integration credentials",
        status: "active",
        isDraft: false,
        sourceBranch: "refs/heads/feature/rotation",
        targetBranch: "refs/heads/main",
        author: { id: "user-1", displayName: "Ada Reviewer" },
        projectId: "project-1",
        projectName: "Platform",
        repositoryId: "azure-repo-1",
        repositoryName: "api",
        webUrl: "https://dev.azure.com/acme/Platform/_git/api/pullrequest/42",
        apiUrl: "https://dev.azure.com/acme/Platform/_apis/git/pullrequests/42",
      },
      reviewers: [
        {
          id: "user-2",
          displayName: "Grace Reviewer",
          vote: 10,
          isRequired: true,
          hasDeclined: false,
        },
      ],
      threads: [],
      linkedWorkItems: [],
      policies: [],
      reviewState: "approved",
      policyState: "success",
    },
  },
};

test("mobile settings explain PAT scopes and link to the organization token page", async ({
  seedData,
  testPage,
}) => {
  await testPage.goto(
    `/settings/workspace/${encodeURIComponent(seedData.workspaceId)}/integrations/azure-devops`,
  );

  await testPage.getByTestId("azure-devops-organization").fill("https://dev.azure.com/acme");
  await testPage.getByRole("button", { name: "How to create a personal access token" }).click();
  const createTokenLink = testPage.getByTestId("azure-devops-pat-help").locator(":scope > a");
  await expect(createTokenLink).toBeVisible();
  await expect(createTokenLink).toHaveAttribute(
    "href",
    "https://dev.azure.com/acme/_usersSettings/tokens",
  );
  await expect(testPage.getByTestId("azure-devops-pat-help")).toContainText(
    "Leave all other scopes unchecked",
  );

  const viewportFits = await testPage.evaluate(
    () => document.documentElement.scrollWidth <= window.innerWidth,
  );
  expect(viewportFits).toBe(true);
});

test("mobile board opens a focused column editor without horizontal overflow", async ({
  apiClient,
  seedData,
  testPage,
}) => {
  await apiClient.mockAzureDevOpsSeed(MOCK_STATE);
  await apiClient.setAzureDevOpsConfig(seedData.workspaceId, {
    organizationUrl: "https://dev.azure.com/acme",
    pat: "azure-test-pat",
  });
  await testPage.goto("/azure-devops");

  await expect(testPage.getByTestId("azure-devops-board")).toBeVisible();
  await expect(testPage.getByText("Handle token rotation")).toBeVisible();
  await testPage.getByTestId("azure-board-card-101").click();
  await expect(testPage.getByRole("heading", { name: /Edit work item/ })).toBeVisible();
  await testPage.getByRole("button", { name: "Close" }).click();

  await testPage.getByTestId("azure-devops-work-items-mode").click();
  await testPage.getByTestId("azure-devops-mobile-filter-button").click();
  await testPage.getByTestId("azure-devops-search-button-mobile").click();
  await testPage.getByTestId("azure-devops-pull-requests-mode").click();
  await testPage.getByTestId("azure-devops-mobile-filter-button").click();
  await testPage.getByTestId("azure-devops-search-button-mobile").click();
  await expect(testPage.getByText("Rotate integration credentials")).toBeVisible();
  await testPage.getByRole("button", { name: "Feedback" }).click();
  await expect(testPage.getByTestId("azure-devops-feedback-detail")).toContainText(
    "Grace Reviewer",
  );

  const viewportFits = await testPage.evaluate(
    () => document.documentElement.scrollWidth <= window.innerWidth,
  );
  expect(viewportFits).toBe(true);
});
