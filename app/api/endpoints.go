package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func segment(value string) string {
	switch value {
	case ".":
		return "%2E"
	case "..":
		return "%2E%2E"
	default:
		return url.PathEscape(value)
	}
}

func queryStrings(params ...string) url.Values {
	q := url.Values{}
	for i := 0; i < len(params); i += 2 {
		if params[i+1] != "" {
			q.Set(params[i], params[i+1])
		}
	}
	return q
}

func setPositive(q url.Values, key string, value int) {
	if value > 0 {
		q.Set(key, strconv.Itoa(value))
	}
}

func (opts KnowledgePageOptions) query() url.Values {
	q := queryStrings("project_id", opts.ProjectID, "sort", opts.Sort, "category", opts.Category,
		"common_category", opts.CommonCategory, "recall_agent_id", opts.RecallAgentID, "recall_event_id", opts.RecallEventID,
		"cursor", opts.Cursor, "direction", opts.Direction, "anchor_doc_id", opts.AnchorDocID)
	setPositive(q, "limit", opts.Limit)
	if opts.UnreadOnly {
		q.Set("unread_only", "true")
	}
	return q
}

func (opts RawRecordsOptions) query() url.Values {
	q := queryStrings("project_id", opts.ProjectID, "kind", opts.Kind, "user_id", opts.UserID,
		"cursor", opts.Cursor, "direction", opts.Direction, "anchor_doc_id", opts.AnchorDocID)
	setPositive(q, "limit", opts.Limit)
	return q
}

func (c *Client) OverviewBootstrap(ctx context.Context, opts OverviewOptions) (OverviewSnapshot, error) {
	q := url.Values{}
	setPositive(q, "days", opts.Days)
	setPositive(q, "recent_limit", opts.RecentLimit)
	setPositive(q, "recall_limit", opts.RecallLimit)
	setPositive(q, "search_limit", opts.SearchLimit)
	return requestValue[OverviewSnapshot](ctx, c, http.MethodGet, "/api/overview/bootstrap", q, nil)
}

func (c *Client) OverviewDay(ctx context.Context, day string, opts KnowledgePageOptions) (KnowledgePage, error) {
	q := opts.query()
	q.Set("day", day)
	return requestValue[KnowledgePage](ctx, c, http.MethodGet, "/api/overview/day", q, nil)
}

func projectPath(id string) string { return "/api/projects/" + segment(id) }

func (c *Client) ListProjects(ctx context.Context) (ListProjectsResponse, error) {
	return requestValue[ListProjectsResponse](ctx, c, http.MethodGet, "/api/projects", nil, nil)
}

func (c *Client) GetProject(ctx context.Context, id string) (ProjectView, error) {
	return requestValue[ProjectView](ctx, c, http.MethodGet, projectPath(id), nil, nil)
}

func (c *Client) CreateProject(ctx context.Context, input CreateProjectRequest) (ProjectView, error) {
	return requestValue[ProjectView](ctx, c, http.MethodPost, "/api/projects", nil, input)
}

func (c *Client) UpdateProject(ctx context.Context, id string, input UpdateProjectRequest) (ProjectView, error) {
	return requestValue[ProjectView](ctx, c, http.MethodPatch, projectPath(id), nil, input)
}

func (c *Client) RenameProject(ctx context.Context, id, name string) (ProjectView, error) {
	return requestValue[ProjectView](ctx, c, http.MethodPut, projectPath(id)+"/name", nil, map[string]string{"name": name})
}

func (c *Client) SetProjectAutoCapture(ctx context.Context, id string, enabled bool) (ProjectView, error) {
	return requestValue[ProjectView](ctx, c, http.MethodPut, projectPath(id)+"/auto-capture", nil, map[string]bool{"auto_capture": enabled})
}

func (c *Client) AddProjectAlias(ctx context.Context, id, path string) (ProjectView, error) {
	return requestValue[ProjectView](ctx, c, http.MethodPost, projectPath(id)+"/aliases", nil, map[string]string{"path": path})
}

func (c *Client) DeleteProject(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, projectPath(id), nil, nil, nil)
}

func (c *Client) RetryProject(ctx context.Context, id string) (RetryProjectResponse, error) {
	return requestValue[RetryProjectResponse](ctx, c, http.MethodPost, projectPath(id)+"/retry", nil, nil)
}

func (c *Client) SetProjectACL(ctx context.Context, id, agentID string, input ACLOverride) (ACLOverride, error) {
	input.AgentID = agentID
	return requestValue[ACLOverride](ctx, c, http.MethodPut, projectPath(id)+"/acl/"+segment(agentID), nil, input)
}

func (c *Client) DeleteProjectACL(ctx context.Context, id, agentID string) error {
	return c.request(ctx, http.MethodDelete, projectPath(id)+"/acl/"+segment(agentID), nil, nil, nil)
}

func (c *Client) ListKnowledge(ctx context.Context, opts KnowledgePageOptions) (KnowledgePage, error) {
	return requestValue[KnowledgePage](ctx, c, http.MethodGet, "/api/knowledge", opts.query(), nil)
}

func knowledgePath(ds, doc string) string {
	return "/api/knowledge/" + segment(ds) + "/" + segment(doc)
}

func (c *Client) GetKnowledgeDocument(ctx context.Context, ds, doc string) (DocumentDetail, error) {
	return requestValue[DocumentDetail](ctx, c, http.MethodGet, knowledgePath(ds, doc), nil, nil)
}

func (c *Client) GetKnowledgeDocumentBody(ctx context.Context, ds, doc string) (DocumentBody, error) {
	return requestValue[DocumentBody](ctx, c, http.MethodGet, knowledgePath(ds, doc)+"/body", nil, nil)
}

func (c *Client) DeleteKnowledgeDocument(ctx context.Context, ds, doc string) error {
	return c.request(ctx, http.MethodDelete, knowledgePath(ds, doc), nil, nil, nil)
}

func (c *Client) ListRawRecords(ctx context.Context, opts RawRecordsOptions) (RawRecordsPage, error) {
	return requestValue[RawRecordsPage](ctx, c, http.MethodGet, "/api/raw-records", opts.query(), nil)
}

func (c *Client) ListSessionChildren(ctx context.Context, projectID, sessionID string, opts RawRecordsOptions) (RawRecordsPage, error) {
	path := projectPath(projectID) + "/raw-records/sessions/" + segment(sessionID) + "/children"
	return requestValue[RawRecordsPage](ctx, c, http.MethodGet, path, opts.query(), nil)
}

func (c *Client) GetSession(ctx context.Context, ds, sessionID, userID string) (RawItem, error) {
	path := "/api/datasets/" + segment(ds) + "/sessions/" + segment(sessionID)
	return requestValue[RawItem](ctx, c, http.MethodGet, path, queryStrings("user_id", userID), nil)
}

func (c *Client) ListCommonMemory(ctx context.Context, opts KnowledgePageOptions) (KnowledgePage, error) {
	return requestValue[KnowledgePage](ctx, c, http.MethodGet, "/api/common", opts.query(), nil)
}

func (c *Client) GetCommonMemory(ctx context.Context, doc string) (DocumentDetail, error) {
	return requestValue[DocumentDetail](ctx, c, http.MethodGet, "/api/common/"+segment(doc), nil, nil)
}

func (c *Client) CreateCommonMemory(ctx context.Context, input SaveCommonMemoryRequest) (SaveCommonMemoryResponse, error) {
	return requestValue[SaveCommonMemoryResponse](ctx, c, http.MethodPost, "/api/common", nil, input)
}

func (c *Client) UpdateCommonMemory(ctx context.Context, doc string, input SaveCommonMemoryRequest) (SaveCommonMemoryResponse, error) {
	return requestValue[SaveCommonMemoryResponse](ctx, c, http.MethodPatch, "/api/common/"+segment(doc), nil, input)
}

func (c *Client) DeleteCommonMemory(ctx context.Context, doc string) error {
	return c.request(ctx, http.MethodDelete, "/api/common/"+segment(doc), nil, nil, nil)
}

func (c *Client) SetCommonACL(ctx context.Context, agentID string, access AccessLevel) (ACLOverride, error) {
	return requestValue[ACLOverride](ctx, c, http.MethodPut, "/api/common/acl/"+segment(agentID), nil, ACLOverride{AgentID: agentID, Access: &access})
}

func (c *Client) Search(ctx context.Context, input SearchRequest) (SearchResponse, error) {
	return requestValue[SearchResponse](ctx, c, http.MethodPost, "/api/search", nil, input)
}

func (c *Client) RecentSearch(ctx context.Context, limit int) (RecentSearchResponse, error) {
	q := url.Values{}
	setPositive(q, "limit", limit)
	return requestValue[RecentSearchResponse](ctx, c, http.MethodGet, "/api/search/recent", q, nil)
}

func (c *Client) ReadingState(ctx context.Context, projectID string) (ReadingStateResponse, error) {
	return requestValue[ReadingStateResponse](ctx, c, http.MethodGet, "/api/reading-state", queryStrings("project_id", projectID), nil)
}

func (c *Client) MarkRead(ctx context.Context, input MarkReadRequest) (MarkReadResponse, error) {
	return requestValue[MarkReadResponse](ctx, c, http.MethodPost, "/api/reading-state/mark-read", nil, input)
}

func (c *Client) MarkAllRead(ctx context.Context, input MarkReadRequest) (MarkReadResponse, error) {
	return requestValue[MarkReadResponse](ctx, c, http.MethodPost, "/api/reading-state/mark-all-read", nil, input)
}

func (c *Client) Graph(ctx context.Context, projectID, datasetID, filter string) (GraphResponse, error) {
	q := queryStrings("project_id", projectID, "dataset_id", datasetID, "filter", filter)
	return requestValue[GraphResponse](ctx, c, http.MethodGet, "/api/graph", q, nil)
}

func (c *Client) GetGeneration(ctx context.Context) (GenerationOverviewResponse, error) {
	return requestValue[GenerationOverviewResponse](ctx, c, http.MethodGet, "/api/generation", nil, nil)
}

func (c *Client) CreateGenerationProfile(ctx context.Context, input SaveGenerationProfileRequest) (GenerationProfile, error) {
	return requestValue[GenerationProfile](ctx, c, http.MethodPost, "/api/generation/profiles", nil, input)
}

func (c *Client) SaveGenerationProfile(ctx context.Context, id string, input SaveGenerationProfileRequest) (GenerationProfile, error) {
	return requestValue[GenerationProfile](ctx, c, http.MethodPut, "/api/generation/profiles/"+segment(id), nil, input)
}

func (c *Client) DeleteGenerationProfile(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/api/generation/profiles/"+segment(id), nil, nil, nil)
}

func (c *Client) ValidateGenerationProfile(ctx context.Context, input ProfileSelection) (ProbeResult, error) {
	return requestValue[ProbeResult](ctx, c, http.MethodPost, "/api/generation/validate", nil, input)
}

func (c *Client) ActivateGenerationProfile(ctx context.Context, input ProfileSelection) (ActivateProfileResponse, error) {
	return requestValue[ActivateProfileResponse](ctx, c, http.MethodPost, "/api/generation/activate", nil, input)
}

func (c *Client) ListAgents(ctx context.Context) (ListAgentsResponse, error) {
	return requestValue[ListAgentsResponse](ctx, c, http.MethodGet, "/api/agents", nil, nil)
}

func (c *Client) ConnectAgent(ctx context.Context, input ConnectAgentRequest) (AgentView, error) {
	return requestValue[AgentView](ctx, c, http.MethodPost, "/api/agents", nil, input)
}

func (c *Client) RetireAgent(ctx context.Context, id string) (AgentView, error) {
	return requestValue[AgentView](ctx, c, http.MethodPost, "/api/agents/"+segment(id)+"/retire", nil, nil)
}

func (c *Client) AgentConflicts(ctx context.Context, kind string) (AgentConflictsResponse, error) {
	return requestValue[AgentConflictsResponse](ctx, c, http.MethodGet, "/api/agents/conflicts", queryStrings("kind", kind), nil)
}

func profilePath(source string) string { return "/api/profiles/" + segment(source) }

func (c *Client) ListUserProfiles(ctx context.Context) (ListUserProfilesResponse, error) {
	return requestValue[ListUserProfilesResponse](ctx, c, http.MethodGet, "/api/profiles", nil, nil)
}

func (c *Client) GetUserProfile(ctx context.Context, source string) (UserProfileView, error) {
	return requestValue[UserProfileView](ctx, c, http.MethodGet, profilePath(source), nil, nil)
}

func (c *Client) StartUserProfileImport(ctx context.Context, source, operationID string) (ProfileImportResponse, error) {
	return requestValue[ProfileImportResponse](ctx, c, http.MethodPost, profilePath(source)+"/import", nil, map[string]string{"operation_id": operationID})
}

func (c *Client) SaveManualUserProfile(ctx context.Context, source, operationID, body string) (UserProfileView, error) {
	return requestValue[UserProfileView](ctx, c, http.MethodPost, profilePath(source)+"/manual", nil, map[string]string{"operation_id": operationID, "body": body})
}

func (c *Client) CancelUserProfileImport(ctx context.Context, source, operationID string) (ProfileCancelResponse, error) {
	return requestValue[ProfileCancelResponse](ctx, c, http.MethodPost, profilePath(source)+"/cancel", nil, map[string]string{"operation_id": operationID})
}

func (c *Client) DeleteUserProfile(ctx context.Context, source string) (ProfileDeleteResponse, error) {
	return requestValue[ProfileDeleteResponse](ctx, c, http.MethodDelete, profilePath(source), nil, nil)
}
