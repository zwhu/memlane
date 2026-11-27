package recall

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

type ScopeKind string

const (
	ScopeKindProject ScopeKind = "project"
	ScopeKindCode    ScopeKind = "code"
	ScopeKindCommon  ScopeKind = "common"
)

type AuthorizedScope struct {
	engine.Scope
	ProjectID string    `json:"project_id,omitempty"`
	Kind      ScopeKind `json:"kind"`
}

type ScopeError struct {
	DatasetID string    `json:"dataset_id"`
	ScopeKind ScopeKind `json:"scope_kind"`
	Code      string    `json:"code"`
	Message   string    `json:"message"`
}

type ResolveInput struct {
	Store       *domain.Store
	Agent       domain.Agent
	UserID      string
	Projects    []domain.Project
	Explicit    bool
	DatasetIDs  []string
	CodeOnly    bool
	IncludeCode bool
	AllowEmpty  bool
}

type ResolveOutput struct {
	Scopes          []AuthorizedScope
	CommonIncluded  bool
	CommonDatasetID string
}

// AgentUserID returns the canonical UUID for agent.PrincipalID when valid, or "" for default owner scope.
func AgentUserID(agent domain.Agent) string {
	principalID := strings.TrimSpace(agent.PrincipalID)
	if id, err := uuid.Parse(principalID); err == nil {
		return id.String()
	}
	return ""
}

// IsProtectedProject returns true if the project name, dataset name, ID, or dataset ID is protected.
func IsProtectedProject(project domain.Project) bool {
	return domain.IsProtectedProject(project.Name, project.DatasetName) ||
		domain.IsProtectedProject(project.ID, project.DatasetID)
}

func ResolveScopes(ctx context.Context, in ResolveInput) (ResolveOutput, error) {
	projects := in.Projects
	if !in.Explicit && projects == nil {
		var err error
		projects, err = in.Store.ListProjects(ctx)
		if err != nil {
			return ResolveOutput{}, err
		}
	}
	if in.Explicit {
		if len(projects) == 0 {
			return ResolveOutput{}, domain.ErrForbidden
		}
		for _, p := range projects {
			if IsProtectedProject(p) {
				return ResolveOutput{}, domain.ErrForbidden
			}
			acl, err := in.Store.EvaluateProjectACL(ctx, p.ID, in.Agent.ID)
			if err != nil {
				return ResolveOutput{}, err
			}
			if acl.Access != domain.AccessRead && acl.Access != domain.AccessMaintain {
				return ResolveOutput{}, domain.ErrForbidden
			}
		}
	}

	type target struct {
		project   domain.Project
		code      bool
		protected bool
	}
	targets := make(map[string][]target)
	for _, p := range projects {
		prot := IsProtectedProject(p)
		if ds := strings.TrimSpace(p.DatasetID); ds != "" {
			targets[ds] = append(targets[ds], target{project: p, code: false, protected: prot || domain.IsProtectedProject("", ds)})
		}
		if p.CodeEnabled {
			if cds := strings.TrimSpace(p.CodeDatasetID); cds != "" {
				targets[cds] = append(targets[cds], target{project: p, code: true, protected: prot || domain.IsProtectedProject("", cds)})
			}
		}
	}

	common, err := in.Store.GetCommonConfig(ctx)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return ResolveOutput{}, err
	}
	commonDatasetID := strings.TrimSpace(common.DatasetID)
	if commonDatasetID != "" && (domain.IsProtectedProject(common.Name, common.DatasetName) || domain.IsProtectedProject("", commonDatasetID)) {
		commonDatasetID = ""
	}

	ids := in.DatasetIDs
	if len(ids) == 0 {
		for _, p := range projects {
			if in.CodeOnly {
				if p.CodeEnabled && strings.TrimSpace(p.CodeDatasetID) != "" {
					ids = append(ids, strings.TrimSpace(p.CodeDatasetID))
				}
				continue
			}
			if strings.TrimSpace(p.DatasetID) != "" {
				ids = append(ids, strings.TrimSpace(p.DatasetID))
			}
			if in.IncludeCode && p.CodeEnabled && strings.TrimSpace(p.CodeDatasetID) != "" {
				ids = append(ids, strings.TrimSpace(p.CodeDatasetID))
			}
		}
	}

	userID := strings.TrimSpace(in.UserID)
	scopes := make([]AuthorizedScope, 0, len(ids)+1)
	seen := make(map[string]bool)
	allCode := true
	commonRequested := false

	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		if id == commonDatasetID {
			commonRequested = true
			continue
		}
		if domain.IsProtectedProject("", id) {
			if in.Explicit || len(in.DatasetIDs) > 0 {
				return ResolveOutput{}, domain.ErrForbidden
			}
			continue
		}
		matchedTargets := targets[id]
		if len(matchedTargets) != 1 {
			return ResolveOutput{}, domain.ErrForbidden
		}
		t := matchedTargets[0]
		if t.protected {
			if in.Explicit || len(in.DatasetIDs) > 0 {
				return ResolveOutput{}, domain.ErrForbidden
			}
			continue
		}
		acl, err := in.Store.EvaluateProjectACL(ctx, t.project.ID, in.Agent.ID)
		if err != nil {
			return ResolveOutput{}, err
		}
		if acl.Access != domain.AccessRead && acl.Access != domain.AccessMaintain {
			if in.Explicit || len(in.DatasetIDs) > 0 {
				return ResolveOutput{}, domain.ErrForbidden
			}
			continue
		}
		seen[id] = true
		kind := ScopeKindProject
		if t.code {
			kind = ScopeKindCode
		}
		allCode = allCode && t.code
		scopes = append(scopes, AuthorizedScope{
			Scope:     engine.Scope{DatasetID: id, UserID: userID},
			ProjectID: t.project.ID,
			Kind:      kind,
		})
	}

	if in.Explicit && len(scopes) == 0 && !commonRequested {
		return ResolveOutput{}, domain.ErrForbidden
	}

	includeCommon := false
	commonEligible := !in.CodeOnly && !(len(scopes) > 0 && allCode)
	if commonDatasetID != "" && (commonRequested || commonEligible) {
		acl, err := in.Store.EvaluateCommonACL(ctx, in.Agent.ID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return ResolveOutput{}, err
		}
		commonAllowed := err == nil && (acl.Access == domain.AccessRead || acl.Access == domain.AccessMaintain)
		if commonRequested && !commonAllowed {
			return ResolveOutput{}, domain.ErrForbidden
		}
		if commonEligible && commonAllowed && !seen[commonDatasetID] {
			seen[commonDatasetID] = true
			scopes = append(scopes, AuthorizedScope{
				Scope: engine.Scope{DatasetID: commonDatasetID, UserID: userID},
				Kind:  ScopeKindCommon,
			})
			includeCommon = true
		}
	}

	if len(scopes) == 0 && !in.AllowEmpty {
		return ResolveOutput{}, domain.ErrForbidden
	}
	return ResolveOutput{
		Scopes:          scopes,
		CommonIncluded:  includeCommon,
		CommonDatasetID: commonDatasetID,
	}, nil
}

type SearchOptions struct {
	Engine                engine.MemoryEngine
	Scopes                []AuthorizedScope
	Query                 string
	Mode                  engine.SearchMode
	Limit                 int
	Kind                  string
	NodeSets              []string
	SessionID             string
	IncludeSessionContext bool
	HitFilter             func([]engine.SearchHit) []engine.SearchHit
}

type SearchOutput struct {
	Hits             []engine.SearchHit
	GraphContext     *engine.GraphSearchContext
	DegradedToChunks bool
	Partial          bool
	ScopeErrors      []ScopeError
}

type scopeOutcome struct {
	scope AuthorizedScope
	res   engine.SearchResult
	err   error
}

type scopedGraphPart struct {
	datasetID string
	gc        *engine.GraphSearchContext
}

const maxConcurrentScopeSearches = 4

func Execute(ctx context.Context, opts SearchOptions) (SearchOutput, error) {
	if len(opts.Scopes) == 0 {
		return SearchOutput{Hits: []engine.SearchHit{}}, nil
	}
	limit := opts.Limit
	if limit < 1 {
		limit = 10
	}

	// Only attach session context to the first primary scope so we don't fetch it redundantly.
	sessionCtxIdx := -1
	if opts.IncludeSessionContext && strings.TrimSpace(opts.SessionID) != "" {
		for i, sc := range opts.Scopes {
			if sc.Kind != ScopeKindCommon {
				sessionCtxIdx = i
				break
			}
		}
	}

	outcomes := make([]scopeOutcome, len(opts.Scopes))
	sem := make(chan struct{}, min(maxConcurrentScopeSearches, len(opts.Scopes)))
	var wg sync.WaitGroup
	for i, sc := range opts.Scopes {
		wg.Add(1)
		go func(idx int, scope AuthorizedScope) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			filters := engine.SearchFilters{
				Kind:     opts.Kind,
				Limit:    limit,
				NodeSets: opts.NodeSets,
			}
			if idx == sessionCtxIdx {
				filters.SessionID = opts.SessionID
				filters.IncludeSessionContext = true
			}
			req := engine.SearchRequest{
				Scopes:  []engine.Scope{scope.Scope},
				Query:   opts.Query,
				Mode:    opts.Mode,
				Limit:   limit,
				Filters: filters,
			}
			res, err := opts.Engine.Search(ctx, req)
			if err == nil && ctx.Err() != nil {
				err = ctx.Err()
			}
			if err == nil && !res.OK {
				err = engine.ErrUnavailable
			}
			outcomes[idx] = scopeOutcome{scope: scope, res: res, err: err}
		}(i, sc)
	}
	wg.Wait()

	var (
		primaryTotal   int
		primaryFailed  int
		commonFailed   int
		firstPrimaryEr error
		firstCommonEr  error
		scopeErrors    []ScopeError
		authorizedSet  = make(map[string]bool, len(opts.Scopes))
		primaryHits    []engine.SearchHit
		commonHits     []engine.SearchHit
		seenPrimary    = make(map[[2]string]bool)
		seenCommon     = make(map[[2]string]bool)
		graphParts     []scopedGraphPart
	)

	for _, sc := range opts.Scopes {
		authorizedSet[sc.DatasetID] = true
	}

	for _, out := range outcomes {
		isPrimary := out.scope.Kind != ScopeKindCommon
		if isPrimary {
			primaryTotal++
		}
		if out.err != nil {
			if errors.Is(out.err, engine.ErrDatasetNotProvisioned) {
				// New project dataset has not been provisioned yet: treat as healthy 0-hit result.
				continue
			}
			if isPrimary {
				primaryFailed++
				if firstPrimaryEr == nil {
					firstPrimaryEr = out.err
				}
			} else {
				commonFailed++
				if firstCommonEr == nil {
					firstCommonEr = out.err
				}
			}
			scopeErrors = append(scopeErrors, classifyScopeError(out.scope, out.err))
			continue
		}

		hits := out.res.Hits
		if opts.HitFilter != nil {
			hits = opts.HitFilter(hits)
		}
		for _, hit := range hits {
			if hit.DatasetID != out.scope.DatasetID || !authorizedSet[hit.DatasetID] || strings.TrimSpace(hit.DocumentID) == "" {
				continue
			}
			key := [2]string{hit.DatasetID, hit.DocumentID}
			if isPrimary {
				if !seenPrimary[key] {
					seenPrimary[key] = true
					primaryHits = append(primaryHits, hit)
				}
			} else {
				if !seenCommon[key] {
					seenCommon[key] = true
					commonHits = append(commonHits, hit)
				}
			}
		}
		if out.res.GraphContext != nil {
			graphParts = append(graphParts, scopedGraphPart{datasetID: out.scope.DatasetID, gc: out.res.GraphContext})
		}
	}

	// Fail closed when all requested primary scopes failed (never silently degrade to common-only),
	// or when only common was requested and it failed.
	if primaryTotal > 0 && primaryFailed == primaryTotal {
		return SearchOutput{}, firstPrimaryEr
	}
	if primaryTotal == 0 && commonFailed > 0 {
		return SearchOutput{}, firstCommonEr
	}

	slices.SortStableFunc(primaryHits, compareSearchHits)
	slices.SortStableFunc(commonHits, compareSearchHits)

	mergedHits := mergeHitsWithQuota(primaryHits, commonHits, limit)
	mergedGC, degraded := mergeGraphContexts(graphParts, authorizedSet, limit)

	return SearchOutput{
		Hits:             mergedHits,
		GraphContext:     mergedGC,
		DegradedToChunks: degraded,
		Partial:          len(scopeErrors) > 0,
		ScopeErrors:      scopeErrors,
	}, nil
}

func compareSearchHits(a, b engine.SearchHit) int {
	aMissing := a.ScoreMissing || math.IsNaN(a.Score)
	bMissing := b.ScoreMissing || math.IsNaN(b.Score)
	if aMissing && bMissing {
		return 0
	}
	if aMissing {
		return 1
	}
	if bMissing {
		return -1
	}
	return cmp.Compare(a.Score, b.Score)
}

func mergeHitsWithQuota(primaryHits, commonHits []engine.SearchHit, limit int) []engine.SearchHit {
	if limit <= 0 {
		return []engine.SearchHit{}
	}
	if len(primaryHits) == 0 {
		n := min(len(commonHits), limit)
		out := make([]engine.SearchHit, n)
		copy(out, commonHits[:n])
		return out
	}
	if len(commonHits) == 0 {
		n := min(len(primaryHits), limit)
		out := make([]engine.SearchHit, n)
		copy(out, primaryHits[:n])
		return out
	}

	var primaryTake, commonTake int
	if limit < 3 {
		primaryTake = min(len(primaryHits), limit)
		commonTake = min(len(commonHits), limit-primaryTake)
	} else {
		commonReserve := min(len(commonHits), max(1, limit/5))
		primaryTake = min(len(primaryHits), limit-commonReserve)
		commonTake = min(len(commonHits), limit-primaryTake)
		if primaryTake+commonTake < limit && primaryTake < len(primaryHits) {
			primaryTake = min(len(primaryHits), limit-commonTake)
		}
	}

	out := make([]engine.SearchHit, 0, primaryTake+commonTake)
	out = append(out, primaryHits[:primaryTake]...)
	out = append(out, commonHits[:commonTake]...)
	return out
}

func mergeGraphContexts(parts []scopedGraphPart, authorized map[string]bool, limit int) (*engine.GraphSearchContext, bool) {
	if len(parts) == 0 {
		return nil, false
	}
	var (
		entities      []engine.GraphSearchEntity
		facts         []engine.GraphSearchFact
		conflicts     []engine.GraphSearchConflict
		codeFacts     []engine.CodeSearchFact
		guidance      string
		degraded      bool
		seenFacts     = make(map[[2]string]bool)
		seenConflicts = make(map[[2]string]bool)
		seenCodeFacts = make(map[string]bool)
	)

	for _, part := range parts {
		gc := part.gc
		if gc == nil {
			continue
		}
		if gc.DegradedToChunks {
			degraded = true
		}
		for _, entity := range gc.Entities {
			if entity.DatasetID != part.datasetID || !authorized[entity.DatasetID] {
				continue
			}
			cloned := entity
			if len(entity.Edges) > 0 {
				cloned.Edges = append([]engine.GraphSearchEdge(nil), entity.Edges...)
			}
			if len(entity.Conflicts) > 0 {
				seen := make(map[string]bool, len(entity.Conflicts))
				cleaned := make([]string, 0, len(entity.Conflicts))
				for _, c := range entity.Conflicts {
					if trimmed := strings.TrimSpace(c); trimmed != "" && !seen[trimmed] {
						seen[trimmed] = true
						cleaned = append(cleaned, trimmed)
					}
				}
				cloned.Conflicts = cleaned
			}
			entities = append(entities, cloned)
		}
		for _, fact := range gc.Facts {
			text := strings.TrimSpace(fact.Text)
			if fact.DatasetID != part.datasetID || !authorized[fact.DatasetID] || text == "" {
				continue
			}
			key := [2]string{fact.DatasetID, text}
			if !seenFacts[key] {
				seenFacts[key] = true
				facts = append(facts, engine.GraphSearchFact{DatasetID: fact.DatasetID, Text: text})
			}
		}
		for _, raw := range gc.Conflicts {
			text := strings.TrimSpace(raw.Text)
			if raw.DatasetID != part.datasetID || !authorized[raw.DatasetID] || text == "" {
				continue
			}
			key := [2]string{raw.DatasetID, text}
			if !seenConflicts[key] {
				seenConflicts[key] = true
				conflicts = append(conflicts, engine.GraphSearchConflict{DatasetID: raw.DatasetID, Text: text})
			}
		}
		for _, cf := range gc.CodeFacts {
			name := strings.TrimSpace(cf.Name)
			if cf.DatasetID != part.datasetID || !authorized[cf.DatasetID] || name == "" {
				continue
			}
			key := cf.DatasetID + "\x00" + name + "\x00" + cf.File + "\x00" + strconv.Itoa(cf.Line)
			if seenCodeFacts[key] {
				continue
			}
			seenCodeFacts[key] = true
			cloned := cf
			if len(cf.Relations) > 0 {
				cloned.Relations = append([]engine.CodeSearchRelation(nil), cf.Relations...)
			}
			codeFacts = append(codeFacts, cloned)
		}
		if guidance == "" {
			if trimmed := strings.TrimSpace(gc.SessionGuidance); trimmed != "" {
				guidance = trimmed
			}
		}
	}

	if limit > 0 && len(codeFacts) > limit {
		codeFacts = codeFacts[:limit]
	}
	if len(entities) == 0 && len(facts) == 0 && len(conflicts) == 0 && guidance == "" && len(codeFacts) == 0 {
		return nil, degraded
	}
	return &engine.GraphSearchContext{
		Entities:         entities,
		Facts:            facts,
		Conflicts:        conflicts,
		SessionGuidance:  guidance,
		CodeFacts:        codeFacts,
		DegradedToChunks: degraded,
	}, degraded
}

func classifyScopeError(scope AuthorizedScope, err error) ScopeError {
	code := "engine_error"
	message := "scope search failed"
	var ee *engine.EngineError
	if errors.As(err, &ee) && ee.Code != "" {
		code = ee.Code
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		if code == "engine_error" {
			code = "engine_timeout"
		}
		message = "scope search timed out"
	case errors.Is(err, engine.ErrUnauthorized), errors.Is(err, engine.ErrForbidden):
		if code == "engine_error" {
			code = "forbidden"
		}
		message = "scope access denied"
	case errors.Is(err, engine.ErrUnavailable):
		if code == "engine_error" {
			code = "engine_unavailable"
		}
		message = "engine unavailable for scope"
	case errors.Is(err, engine.ErrNotFound):
		if code == "engine_error" {
			code = "not_found"
		}
		message = "scope dataset not found"
	}
	return ScopeError{
		DatasetID: scope.DatasetID,
		ScopeKind: scope.Kind,
		Code:      code,
		Message:   message,
	}
}
