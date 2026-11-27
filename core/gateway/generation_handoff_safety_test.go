package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	"github.com/zwhu/memlane/kit/agents"
)

func TestExpectedDigestStoreOnlyBlocksRogueEngineDigest(t *testing.T) {
	s, _, fake := testServer(t, nil)
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, http.StatusOK)

	// Engine advertises a rogue/different digest on ModelStatus & Health.
	fake.mu.Lock()
	fake.modelDigest = "rogue-engine-digest-not-in-store"
	fake.mu.Unlock()

	wantDigest, err := s.expectedActiveModelDigest(ctx)
	must(t, err)
	if wantDigest == "" || wantDigest == "rogue-engine-digest-not-in-store" {
		t.Fatalf("expected Store-derived digest, got %q", wantDigest)
	}

	// Simulate Cognee's verify_expected_digest rejecting the mismatch with 409.
	fake.mu.Lock()
	fake.extractErr = &engine.EngineError{
		StatusCode: http.StatusConflict,
		Code:       "model_digest_mismatch",
		Message:    "expected_model_digest does not match active engine digest",
		Err:        engine.ErrModelDigestMismatch,
	}
	fake.mu.Unlock()

	agentRequest(t, s, "/ingest", `{"project_id":"p","session_id":"s-rogue","message_id":"m-rogue","content":{"question":"q","answer":"a"}}`, http.StatusAccepted)
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if worked {
		t.Fatal("expected 409 model_digest_mismatch to requeue item and return worked=false")
	}
	if len(fake.extract) != 1 {
		t.Fatalf("extract calls = %d, want 1", len(fake.extract))
	}
	if fake.extract[0].ExpectedModelDigest != wantDigest {
		t.Fatalf("Extract received ExpectedModelDigest=%q, want Store digest %q (must never endorse engine digest)",
			fake.extract[0].ExpectedModelDigest, wantDigest)
	}
	summary, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	if summary.Pending != 1 || summary.Failed != 0 || summary.Succeeded != 0 {
		t.Fatalf("expected 409 mismatch to preserve pending item without failing, summary=%+v", summary)
	}
	// Backoff must prevent tight spinning on immediate retry.
	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if worked || len(fake.extract) != 1 {
		t.Fatalf("expected backoff to suppress immediate retry: worked=%v extracts=%d", worked, len(fake.extract))
	}
}

func TestStartupApplyFailurePausesExtractionUntilRecovered(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, http.StatusOK)

	fake.mu.Lock()
	fake.applyErr = &engine.EngineError{Code: "unavailable", Message: "engine down", Err: engine.ErrUnavailable}
	fake.mu.Unlock()

	if err := s.ApplyActiveGeneration(ctx); err == nil {
		t.Fatal("expected ApplyActiveGeneration to fail when engine is unavailable")
	}
	s.RecordStartupGenerationFailure(ctx, errors.New("startup_model_apply_failed"))

	agentRequest(t, s, "/ingest", `{"project_id":"p","session_id":"s-start","message_id":"m-start","content":{"question":"q","answer":"a"}}`, http.StatusAccepted)

	// Extraction must remain paused and keep the item pending (zero data loss).
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if worked {
		t.Fatal("expected ProcessOnce to pause while startup_error is set")
	}
	if len(fake.extract) != 0 {
		t.Fatalf("Extract was called while startup model was unverified: %+v", fake.extract)
	}
	summary, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	if summary.Pending != 1 || summary.Failed != 0 || summary.Succeeded != 0 {
		t.Fatalf("expected item preserved as pending, got %+v", summary)
	}

	// Recover startup model apply: must generate a fresh unique operation_id and resume extraction.
	fake.mu.Lock()
	fake.applyErr = nil
	fake.mu.Unlock()
	must(t, s.ApplyActiveGeneration(ctx))
	if len(fake.apply) != 2 || fake.apply[0].OperationID == fake.apply[1].OperationID || !strings.HasPrefix(fake.apply[1].OperationID, "startup-") {
		t.Fatalf("expected distinct startup-* operation_ids, got %+v", fake.apply)
	}

	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked || len(fake.extract) != 1 {
		t.Fatalf("expected extraction to resume after startup recovery: worked=%v extracts=%d", worked, len(fake.extract))
	}
	state, err := store.GetGenerationState(ctx)
	must(t, err)
	if state.StartupError != "" {
		t.Fatalf("expected startup_error cleared, got %q", state.StartupError)
	}
}

func TestHotSwitchTransportErrorResultUnknownPausesAndReconciles(t *testing.T) {
	ctx := context.Background()

	t.Run("transport error engine switched then reconciled to applied", func(t *testing.T) {
		s, store, fake := testServer(t, nil)
		manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, http.StatusOK)

		nextProfile, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
			ProfileID:  "agent-traex",
			Mode:       "agent_bridge",
			BridgeKind: "traex",
			Model:      "traex-v2",
		})
		must(t, err)
		targetDigest, err := s.expectedProfileDigest(ctx, nextProfile.ProfileID, nextProfile.Revision)
		must(t, err)

		// Engine switches internally but gateway loses the HTTP reply (transport error).
		fake.mu.Lock()
		fake.applyErr = &engine.EngineError{Code: "engine_timeout", Message: "connection reset", Err: errors.Join(engine.ErrUnavailable, context.DeadlineExceeded)}
		fake.modelDigest = "" // Engine briefly unreachable for status too.
		fake.mu.Unlock()

		res := manage(t, s, "POST", "/v1/generation/handoffs",
			fmt.Sprintf(`{"profile":{"id":%q,"revision":%d},"operation_id":"op-switch-lost"}`, nextProfile.ProfileID, nextProfile.Revision),
			http.StatusOK)
		body := decodeResponse[map[string]any](t, res)
		handoff := body["handoff"].(map[string]any)
		if handoff["status"] != "unknown" || handoff["reason"] != "model_apply_unconfirmed" {
			t.Fatalf("expected status=unknown reason=model_apply_unconfirmed, got %+v", handoff)
		}

		// Active profile must NOT be mutated while unconfirmed.
		state, err := store.GetGenerationState(ctx)
		must(t, err)
		if state.ActiveProfileID != "default-bridge" {
			t.Fatalf("active profile mutated during result_unknown: %+v", state)
		}

		// GET /api/generation must expose active_handoff so Settings shows "正在确认使用的模型".
		genRes := decodeResponse[map[string]any](t, manage(t, s, "GET", "/api/generation", "", http.StatusOK))
		activeHandoff, ok := genRes["active_handoff"].(map[string]any)
		if !ok || activeHandoff["status"] != "unknown" || activeHandoff["operation_id"] != "op-switch-lost" {
			t.Fatalf("GET /api/generation missing active_handoff: %+v", genRes["active_handoff"])
		}

		// InboxWorker must pause Extract while handoff is result_unknown (preserving pending item).
		agentRequest(t, s, "/ingest", `{"project_id":"p","session_id":"s-unk","message_id":"m-unk","content":{"question":"q","answer":"a"}}`, http.StatusAccepted)
		worked, err := s.Inbox().ProcessOnce(ctx)
		must(t, err)
		if worked || len(fake.extract) != 0 {
			t.Fatalf("expected Extract paused during result_unknown: worked=%v extracts=%d", worked, len(fake.extract))
		}

		// Engine becomes reachable and reports targetDigest -> explicit poll reconciles and commits active!
		fake.mu.Lock()
		fake.modelDigest = targetDigest
		fake.mu.Unlock()

		pollRes := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/generation/handoffs/op-switch-lost", "", http.StatusOK))
		pollHandoff := pollRes["handoff"].(map[string]any)
		if pollHandoff["status"] != "applied" {
			t.Fatalf("expected reconciled status=applied, got %+v", pollHandoff)
		}
		state, err = store.GetGenerationState(ctx)
		must(t, err)
		if state.ActiveProfileID != nextProfile.ProfileID || state.ActiveRevision != nextProfile.Revision {
			t.Fatalf("reconcile did not commit new active profile: %+v", state)
		}

		// InboxWorker resumes and passes the new targetDigest to Extract.
		worked, err = s.Inbox().ProcessOnce(ctx)
		must(t, err)
		if !worked || len(fake.extract) != 1 || fake.extract[0].ExpectedModelDigest != targetDigest {
			t.Fatalf("expected resumed Extract with targetDigest=%q, worked=%v extracts=%+v", targetDigest, worked, fake.extract)
		}
	})

	t.Run("explicit 4xx engine rejection marks not_applied without pausing", func(t *testing.T) {
		s, store, fake := testServer(t, nil)
		manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, http.StatusOK)

		nextProfile, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
			ProfileID:  "agent-traex",
			Mode:       "agent_bridge",
			BridgeKind: "traex",
			Model:      "bad-model",
		})
		must(t, err)

		fake.mu.Lock()
		fake.applyErr = &engine.EngineError{
			StatusCode: http.StatusUnprocessableEntity,
			Code:       "invalid_argument",
			Message:    "invalid generation config",
			Err:        engine.ErrInvalidArgument,
		}
		fake.mu.Unlock()

		res := manage(t, s, "POST", "/v1/generation/handoffs",
			fmt.Sprintf(`{"profile":{"id":%q,"revision":%d},"operation_id":"op-reject-422"}`, nextProfile.ProfileID, nextProfile.Revision),
			http.StatusOK)
		body := decodeResponse[map[string]any](t, res)
		handoff := body["handoff"].(map[string]any)
		if handoff["status"] != "not_applied" || handoff["reason"] != "model_apply_failed" {
			t.Fatalf("expected explicit 4xx rejection -> not_applied, got %+v", handoff)
		}

		// Extraction must continue using the previous active profile without pausing.
		prevDigest, err := s.expectedActiveModelDigest(ctx)
		must(t, err)
		agentRequest(t, s, "/ingest", `{"project_id":"p","session_id":"s-422","message_id":"m-422","content":{"question":"q","answer":"a"}}`, http.StatusAccepted)
		worked, err := s.Inbox().ProcessOnce(ctx)
		must(t, err)
		if !worked || len(fake.extract) != 1 || fake.extract[0].ExpectedModelDigest != prevDigest {
			t.Fatalf("expected extraction to continue with previous digest=%q, worked=%v extracts=%+v", prevDigest, worked, fake.extract)
		}
	})

	t.Run("draining old digest stays unknown until idempotent replay confirms rejection or applied", func(t *testing.T) {
		s, store, fake := testServer(t, nil)
		manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, http.StatusOK)

		prevDigest, err := s.expectedActiveModelDigest(ctx)
		must(t, err)
		nextProfile, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
			ProfileID:  "agent-traex",
			Mode:       "agent_bridge",
			BridgeKind: "traex",
			Model:      "traex-v2",
		})
		must(t, err)

		// Initial apply loses reply while engine is still draining (ModelStatus still shows prevDigest).
		fake.mu.Lock()
		fake.applyErr = &engine.EngineError{Code: "unavailable", Message: "broken pipe", Err: engine.ErrUnavailable}
		fake.modelDigest = prevDigest
		fake.mu.Unlock()

		res := manage(t, s, "POST", "/v1/generation/handoffs",
			fmt.Sprintf(`{"profile":{"id":%q,"revision":%d},"operation_id":"op-drain-busy"}`, nextProfile.ProfileID, nextProfile.Revision),
			http.StatusOK)
		body := decodeResponse[map[string]any](t, res)
		if body["handoff"].(map[string]any)["status"] != "unknown" {
			t.Fatalf("expected unknown before reconcile, got %+v", body)
		}

		// R1-03: During _drain(), ModelStatus still returns prevDigest and replay returns 409 generation_handoff_busy.
		// Reconcile MUST keep status=unknown and NOT mark failed!
		fake.mu.Lock()
		fake.applyErr = &engine.EngineError{
			StatusCode: http.StatusConflict,
			Code:       "conflict",
			Message:    "generation_handoff_busy",
			Err:        engine.ErrConflict,
		}
		fake.mu.Unlock()

		pollBusy := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/generation/handoffs/op-drain-busy", "", http.StatusOK))
		if pollBusy["handoff"].(map[string]any)["status"] != "unknown" {
			t.Fatalf("draining 409 busy with old ModelStatus must stay unknown, got %+v", pollBusy)
		}

		// Once engine finishes and returns explicit 422 rejection on replay, reconcile transitions to not_applied.
		fake.mu.Lock()
		fake.applyErr = &engine.EngineError{
			StatusCode: http.StatusUnprocessableEntity,
			Code:       "invalid_argument",
			Message:    "invalid_generation_config",
			Err:        engine.ErrInvalidArgument,
		}
		fake.mu.Unlock()

		pollRejected := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/generation/handoffs/op-drain-busy", "", http.StatusOK))
		if pollRejected["handoff"].(map[string]any)["status"] != "not_applied" {
			t.Fatalf("expected explicit 422 replay rejection -> not_applied, got %+v", pollRejected)
		}
		state, err := store.GetGenerationState(ctx)
		must(t, err)
		if state.ActiveProfileID != "default-bridge" {
			t.Fatalf("expected previous active profile preserved, got %+v", state)
		}
		agentRequest(t, s, "/ingest", `{"project_id":"p","session_id":"s-prev","message_id":"m-prev","content":{"question":"q","answer":"a"}}`, http.StatusAccepted)
		worked, err := s.Inbox().ProcessOnce(ctx)
		must(t, err)
		if !worked || len(fake.extract) != 1 || fake.extract[0].ExpectedModelDigest != prevDigest {
			t.Fatalf("expected extraction resumed with prevDigest=%q, worked=%v extracts=%+v", prevDigest, worked, fake.extract)
		}
	})
}

func TestComputeApplyModelDigestMatchesPythonCanonicalJSON(t *testing.T) {
	// Reference SHA-256 digests generated by Python's
	// json.dumps(obj, sort_keys=True, ensure_ascii=False, separators=(',', ':'), allow_nan=False).
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{
			name: "temperature_zero",
			cfg:  map[string]any{"mode": "provider", "model": "gpt-5", "temperature": float64(0)},
			want: "4ebf44294c900d15db7e96aedde85cdc35e96a344df3a2ecff81fda0647fd1d8",
		},
		{
			name: "temperature_one",
			cfg:  map[string]any{"mode": "provider", "model": "gpt-5", "temperature": float64(1.0)},
			want: "711e8cfbbe45a3acb5726dc1a2c7aa6791ae350c6e6ef0f606581fc621a36fee",
		},
		{
			name: "temperature_scientific_1e-05",
			cfg:  map[string]any{"mode": "provider", "model": "gpt-5", "temperature": 0.00001},
			want: "0e02969038a271711bd47ccde68adda64de7cf1f67afc9ed26bcd1e9058b8a86",
		},
		{
			name: "temperature_decimal_0.0001",
			cfg:  map[string]any{"mode": "provider", "model": "gpt-5", "temperature": 0.0001},
			want: "474d62f3d15883ca3509bd6bd901075acaed2d925fa36a1284559d1b3aa3232b",
		},
		{
			name: "unicode_line_separators_and_ollama_evidence",
			cfg: map[string]any{
				"mode":  "provider",
				"model": "m\u2028\u2029x",
				"ollama_schema_evidence": map[string]string{
					"status":   "verified",
					"contract": "ollama-native-nonce-v1",
				},
			},
			want: "fe5c73a360fdff7ac6c92fedac2f6db693d1f4dd18f32ddb655018f4c319d565",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := computeApplyModelDigest(tc.cfg)
			must(t, err)
			if got != tc.want {
				t.Fatalf("computeApplyModelDigest(%s) = %q, want Python digest %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestOllamaRevalidationFailureDoesNotMutateActiveDigest(t *testing.T) {
	s, store, _ := testServer(t, nil)
	ctx := context.Background()

	ollama, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID: "ollama-local",
		Mode:      "provider",
		Provider:  "ollama",
		Model:     "qwen3:8b",
		Endpoint:  "http://localhost:11434",
	})
	must(t, err)
	must(t, store.RecordGenerationValidation(ctx, domain.GenerationValidation{
		OperationID:        "val-pass-1",
		ProfileID:          ollama.ProfileID,
		ProfileRevision:    ollama.Revision,
		Digest:             ollama.Digest,
		RegisteredRevision: 1,
		Status:             "passed",
		PlainPassed:        true,
		SchemaPassed:       true,
		QualityPassed:      true,
		PayloadJSON:        `{"ollama_schema_evidence":{"contract":"ollama-native-nonce-v1","status":"verified","model":"ollama_chat/qwen3:8b","endpoint_sha256":"abc"}}`,
	}))

	// Activate Ollama profile -> freezes evidence & applied digest.
	manage(t, s, "POST", "/v1/generation/handoffs",
		fmt.Sprintf(`{"profile":{"id":%q,"revision":%d},"operation_id":"op-ollama-activate"}`, ollama.ProfileID, ollama.Revision),
		http.StatusOK)

	digestBefore, err := s.expectedActiveModelDigest(ctx)
	must(t, err)

	// Re-validating the same revision times out / fails with empty payload_json.
	must(t, store.RecordGenerationValidation(ctx, domain.GenerationValidation{
		OperationID:        "val-timeout-2",
		ProfileID:          ollama.ProfileID,
		ProfileRevision:    ollama.Revision,
		Digest:             ollama.Digest,
		RegisteredRevision: 2,
		Status:             "result_unknown",
		ErrorCode:          "validation_timeout",
		PayloadJSON:        `{}`,
	}))

	// Both in-memory frozen digest and fresh Store-based applyConfig must keep the exact same digest.
	digestAfter, err := s.expectedActiveModelDigest(ctx)
	must(t, err)
	if digestAfter != digestBefore {
		t.Fatalf("failed revalidation mutated active digest: before=%q after=%q", digestBefore, digestAfter)
	}
	s.freezeActiveDigest("", 0, "")
	digestAfterRestart, err := s.expectedProfileDigest(ctx, ollama.ProfileID, ollama.Revision)
	must(t, err)
	if digestAfterRestart != digestBefore {
		t.Fatalf("failed revalidation mutated applyConfig digest: before=%q afterRestart=%q", digestBefore, digestAfterRestart)
	}
}

func TestConcurrentSecondSwitchRejectedWhileHandoffUnconfirmed(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()

	profileB, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID: "agent-traex", Mode: "agent_bridge", BridgeKind: "traex", Model: "traex-b",
	})
	must(t, err)
	profileC, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID: "agent-pi", Mode: "agent_bridge", BridgeKind: "pi", BridgeProvider: "deepseek", Model: "deepseek-c",
	})
	must(t, err)

	fake.mu.Lock()
	fake.applyErr = &engine.EngineError{Code: "engine_timeout", Message: "timeout", Err: errors.Join(engine.ErrUnavailable, context.DeadlineExceeded)}
	fake.modelDigest = ""
	fake.mu.Unlock()

	// First switch A -> B enters result_unknown.
	manage(t, s, "POST", "/v1/generation/handoffs",
		fmt.Sprintf(`{"profile":{"id":%q,"revision":%d},"operation_id":"op-ab"}`, profileB.ProfileID, profileB.Revision),
		http.StatusOK)

	// Second concurrent switch A -> C must be rejected with 409 generation_handoff_in_progress on both routes.
	resHandoff := manage(t, s, "POST", "/v1/generation/handoffs",
		fmt.Sprintf(`{"profile":{"id":%q,"revision":%d},"operation_id":"op-ac-1"}`, profileC.ProfileID, profileC.Revision),
		http.StatusConflict)
	requireCode(t, resHandoff, "generation_handoff_in_progress")

	resActivate := manage(t, s, "POST", "/api/generation/activate",
		fmt.Sprintf(`{"profile_id":%q,"revision":%d,"operation_id":"op-ac-2"}`, profileC.ProfileID, profileC.Revision),
		http.StatusConflict)
	requireCode(t, resActivate, "generation_handoff_in_progress")
}

func TestExtractHandoff409RequeuesItemAndSessionFlushWithBackoff(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, http.StatusOK)

	// Seed a saved capture receipt so session flush needs consolidation.
	_, _, err := store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "flush-key", UserID: "u", DatasetID: "ds", SessionID: "sess-flush",
		MessageID: "m1", PayloadSHA256: "sha1", EntryType: "capture_trace",
	})
	must(t, err)
	must(t, store.MarkCaptureReceiptSaved(ctx, "flush-key", "entry-1"))
	if !s.Inbox().ScheduleSessionFlush("ds", "sess-flush") {
		t.Fatal("failed to schedule session flush")
	}

	fake.mu.Lock()
	fake.extractErr = &engine.EngineError{
		StatusCode: http.StatusConflict,
		Code:       "conflict",
		Message:    "generation_handoff_in_progress",
		Err:        engine.ErrConflict,
	}
	fake.mu.Unlock()

	// Session flush hits 409 -> must NOT be dropped and must enter backoff.
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if worked {
		t.Fatal("expected 409 session flush to pause and return worked=false")
	}
	if !s.Inbox().hasQueuedSessionFlushes() {
		t.Fatal("session flush was dropped on 409 generation_handoff_in_progress")
	}

	// Recover engine and reset backoff -> session flush succeeds.
	fake.mu.Lock()
	fake.extractErr = nil
	fake.mu.Unlock()
	s.Inbox().resetExtractBackoff()

	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked || s.Inbox().hasQueuedSessionFlushes() {
		t.Fatalf("expected session flush to complete after recovery: worked=%v stillQueued=%v", worked, s.Inbox().hasQueuedSessionFlushes())
	}
}

func TestStartupErrorDoesNotBlockPendingHandoffReconcile(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, http.StatusOK)

	// Simulate startup_error set from failed startup apply.
	must(t, store.SetGenerationStartupError(ctx, "startup_model_apply_failed"))
	s.setStartupModelError("startup_model_apply_failed")

	// User initiates switch to profile B, transport drops -> result_unknown.
	profileB, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID: "agent-traex", Mode: "agent_bridge", BridgeKind: "traex", Model: "traex-recovered",
	})
	must(t, err)
	targetDigest, err := s.expectedProfileDigest(ctx, profileB.ProfileID, profileB.Revision)
	must(t, err)

	fake.mu.Lock()
	fake.applyErr = &engine.EngineError{Code: "unavailable", Message: "eof", Err: engine.ErrUnavailable}
	fake.modelDigest = ""
	fake.mu.Unlock()

	manage(t, s, "POST", "/v1/generation/handoffs",
		fmt.Sprintf(`{"profile":{"id":%q,"revision":%d},"operation_id":"op-recover-b"}`, profileB.ProfileID, profileB.Revision),
		http.StatusOK)

	// Engine now reports targetDigest (B actually applied!).
	fake.mu.Lock()
	fake.applyErr = nil
	fake.modelDigest = targetDigest
	fake.mu.Unlock()

	// R1-06: expectedActiveModelDigest must reconcile pending handoff BEFORE checking startup_error,
	// committing B and clearing startup_error so background extraction unblocks!
	agentRequest(t, s, "/ingest", `{"project_id":"p","session_id":"s-rec","message_id":"m-rec","content":{"question":"q","answer":"a"}}`, http.StatusAccepted)
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked || len(fake.extract) != 1 || fake.extract[0].ExpectedModelDigest != targetDigest {
		t.Fatalf("expected startup_error cleared via background reconcile: worked=%v extracts=%+v", worked, fake.extract)
	}
	state, err := store.GetGenerationState(ctx)
	must(t, err)
	if state.StartupError != "" || state.ActiveProfileID != profileB.ProfileID {
		t.Fatalf("expected active=%s startup_error='', got %+v", profileB.ProfileID, state)
	}
}

func TestFollowAgentDefaultsDoesNotBlockExtraction(t *testing.T) {
	home := t.TempDir()
	traeDir := filepath.Join(home, ".trae")
	must(t, os.MkdirAll(filepath.Join(traeDir, "cli"), 0o700))
	must(t, os.WriteFile(filepath.Join(traeDir, "cli", "auth.json"), []byte(`{"token":"fake"}`), 0o600))
	must(t, os.WriteFile(filepath.Join(traeDir, "traecli.toml"), []byte("model = \"GPT-6.1-Sol\"\nmodel_provider = \"trae\"\n"), 0o600))
	exe := filepath.Join(home, "bin", "traecli")
	must(t, os.MkdirAll(filepath.Dir(exe), 0o700))
	must(t, os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o700))

	s, store, fake := testServer(t, func(c *Config) {
		c.DiscoveryOptions = agents.DiscoveryOptions{
			Home:    home,
			AppDirs: []string{},
			Kinds:   []string{agents.HostTraeX},
			LookPath: func(name string) (string, error) {
				if name == "traecli" {
					return exe, nil
				}
				return "", os.ErrNotExist
			},
		}
	})
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, http.StatusOK)

	p, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID:  "agent-traex",
		Mode:       "agent_bridge",
		BridgeKind: agents.HostTraeX,
		Model:      "GPT-6.1-Sol",
	})
	must(t, err)
	must(t, store.SetActiveGeneration(ctx, p.ProfileID, p.Revision))

	digestBefore, err := s.expectedActiveModelDigest(ctx)
	must(t, err)

	// User switches live CLI default model on disk (FollowAgentDefaults: true).
	must(t, os.WriteFile(filepath.Join(traeDir, "traecli.toml"), []byte("model = \"GPT-6.2-Astra\"\nmodel_provider = \"trae\"\n"), 0o600))

	digestAfter, err := s.expectedActiveModelDigest(ctx)
	must(t, err)
	if digestAfter != digestBefore {
		t.Fatalf("expected Store route digest stable under FollowAgentDefaults: before=%q after=%q", digestBefore, digestAfter)
	}

	agentRequest(t, s, "/ingest", `{"project_id":"p","session_id":"s-follow","message_id":"m-follow","content":{"question":"q","answer":"a"}}`, http.StatusAccepted)
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if !worked || len(fake.extract) != 1 || fake.extract[0].ExpectedModelDigest != digestBefore {
		t.Fatalf("FollowAgentDefaults live CLI change blocked extraction: worked=%v extracts=%+v", worked, fake.extract)
	}
}

func TestIngestHandoff409RequeuesPendingWithBackoff(t *testing.T) {
	s, _, fake := testServer(t, nil)
	ctx := context.Background()
	manage(t, s, "PUT", "/api/projects/p/acl/agent", `{"access":"maintain"}`, http.StatusOK)

	fake.mu.Lock()
	fake.ingestErr = &engine.EngineError{
		StatusCode: http.StatusConflict,
		Code:       "conflict",
		Message:    "generation_handoff_in_progress",
		Err:        engine.ErrConflict,
	}
	fake.mu.Unlock()

	agentRequest(t, s, "/ingest", `{"project_id":"p","session_id":"s-ing409","message_id":"m-ing409","content":{"question":"q","answer":"a"}}`, http.StatusAccepted)
	worked, err := s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if worked {
		t.Fatal("expected 409 on Ingest to requeue and return worked=false")
	}
	summary, err := s.Inbox().Summary(ctx, "p")
	must(t, err)
	if summary.Pending != 1 || summary.Failed != 0 || summary.Succeeded != 0 {
		t.Fatalf("expected item preserved as pending on Ingest 409, got %+v", summary)
	}
	// Backoff must suppress immediate retry.
	worked, err = s.Inbox().ProcessOnce(ctx)
	must(t, err)
	if worked || len(fake.ingest) != 1 {
		t.Fatalf("expected backoff after Ingest 409: worked=%v ingests=%d", worked, len(fake.ingest))
	}
}

func TestGenerationStopUnconfirmedMarksNotApplied(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()

	nextProfile, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID: "agent-traex", Mode: "agent_bridge", BridgeKind: "traex", Model: "traex-stop",
	})
	must(t, err)

	fake.mu.Lock()
	fake.applyErr = &engine.EngineError{
		StatusCode: http.StatusConflict,
		Code:       "generation_stop_unconfirmed",
		Message:    "generation_stop_unconfirmed",
		Err:        engine.ErrConflict,
	}
	fake.mu.Unlock()

	res := manage(t, s, "POST", "/v1/generation/handoffs",
		fmt.Sprintf(`{"profile":{"id":%q,"revision":%d},"operation_id":"op-stop-unconfirmed"}`, nextProfile.ProfileID, nextProfile.Revision),
		http.StatusOK)
	body := decodeResponse[map[string]any](t, res)
	handoff := body["handoff"].(map[string]any)
	if handoff["status"] != "not_applied" || handoff["reason"] != "model_apply_failed" {
		t.Fatalf("expected 409 generation_stop_unconfirmed -> not_applied, got %+v", handoff)
	}
}

func TestStartupApplyReconcilesPendingHandoffBeforeRestoringOldActive(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()

	profileB, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID: "agent-traex", Mode: "agent_bridge", BridgeKind: "traex", Model: "traex-b",
	})
	must(t, err)
	digestB, err := s.expectedProfileDigest(ctx, profileB.ProfileID, profileB.Revision)
	must(t, err)

	// Record a result_unknown handoff A -> B while live engine already switched to B.
	must(t, store.UpsertGenerationHandoff(ctx, domain.GenerationHandoff{
		OperationID: "op-restart-b", ProfileID: profileB.ProfileID, ProfileRevision: profileB.Revision,
		Digest: profileB.Digest, PreviousProfileID: "default-bridge", PreviousRevision: 1,
		Status: "result_unknown", Reason: "model_apply_unconfirmed",
	}))
	fake.mu.Lock()
	fake.modelDigest = digestB
	fake.mu.Unlock()

	// ApplyActiveGeneration must reconcile pending handoff first, committing B instead of overwriting B with old A.
	must(t, s.ApplyActiveGeneration(ctx))
	state, err := store.GetGenerationState(ctx)
	must(t, err)
	if state.ActiveProfileID != profileB.ProfileID || state.ActiveRevision != profileB.Revision {
		t.Fatalf("ApplyActiveGeneration overwrote live B with old A: %+v", state)
	}
	if len(fake.apply) == 0 || fake.apply[len(fake.apply)-1].GenerationConfig["model"] != "traex-b" {
		t.Fatalf("expected startup ApplyModel to apply reconciled B, got %+v", fake.apply)
	}
}

func TestReconcileReplayRequiresLiveModelStatusMatch(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()

	digestA, err := s.expectedActiveModelDigest(ctx)
	must(t, err)
	profileB, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID: "agent-traex", Mode: "agent_bridge", BridgeKind: "traex", Model: "traex-cached",
	})
	must(t, err)
	digestB, err := s.expectedProfileDigest(ctx, profileB.ProfileID, profileB.Revision)
	must(t, err)

	// Seed a result_unknown handoff for B. Engine's ApplyModel returns a stale cached receipt
	// (applied: true), while live ModelStatus is still on digestA!
	must(t, store.UpsertGenerationHandoff(ctx, domain.GenerationHandoff{
		OperationID: "op-stale-cache", ProfileID: profileB.ProfileID, ProfileRevision: profileB.Revision,
		Digest: profileB.Digest, PreviousProfileID: "default-bridge", PreviousRevision: 1,
		Status: "result_unknown", Reason: "model_apply_unconfirmed",
	}))
	fake.mu.Lock()
	fake.applyErr = nil
	fake.keepDigestOnApply = true
	fake.modelDigest = digestA // Engine is actually still on A!
	fake.mu.Unlock()

	poll := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/generation/handoffs/op-stale-cache", "", http.StatusOK))
	if poll["handoff"].(map[string]any)["status"] != "unknown" {
		t.Fatalf("stale cached replay receipt must not commit B when live ModelStatus is A: %+v", poll)
	}
	state, err := store.GetGenerationState(ctx)
	must(t, err)
	if state.ActiveProfileID != "default-bridge" {
		t.Fatalf("active profile wrongly mutated to B despite live ModelStatus=A: %+v", state)
	}

	// Once live ModelStatus confirms digestB, reconcile commits B cleanly.
	fake.mu.Lock()
	fake.modelDigest = digestB
	fake.mu.Unlock()
	poll2 := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/generation/handoffs/op-stale-cache", "", http.StatusOK))
	if poll2["handoff"].(map[string]any)["status"] != "applied" {
		t.Fatalf("expected applied once live ModelStatus matches digestB: %+v", poll2)
	}
}

func TestRemoveProfileBlockedByPendingHandoffAndOrphanUnlocked(t *testing.T) {
	s, store, fake := testServer(t, nil)
	ctx := context.Background()

	digestA, err := s.expectedActiveModelDigest(ctx)
	must(t, err)
	profileB, err := store.PutGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID: "agent-traex", Mode: "agent_bridge", BridgeKind: "traex", Model: "traex-orphan",
	})
	must(t, err)

	must(t, store.UpsertGenerationHandoff(ctx, domain.GenerationHandoff{
		OperationID: "op-ref-b", ProfileID: profileB.ProfileID, ProfileRevision: profileB.Revision,
		Digest: profileB.Digest, PreviousProfileID: "default-bridge", PreviousRevision: 1,
		Status: "result_unknown", Reason: "model_apply_unconfirmed",
	}))

	// Deleting a profile referenced by a pending handoff must be rejected with 409.
	delRes := manage(t, s, "DELETE", "/api/generation/profiles/"+profileB.ProfileID, "", http.StatusConflict)
	requireCode(t, delRes, "pending_handoff_profile_cannot_be_removed")

	// Simulate a legacy DB where profileB was already marked removed before this guard existed.
	_, err = store.DB().ExecContext(ctx, `UPDATE generation_profiles SET removed=1 WHERE profile_id=?`, profileB.ProfileID)
	must(t, err)
	fake.mu.Lock()
	fake.modelDigest = digestA
	fake.mu.Unlock()

	// Reconcile must unlock the orphaned handoff as not_applied instead of deadlocking forever.
	poll := decodeResponse[map[string]any](t, manage(t, s, "GET", "/v1/generation/handoffs/op-ref-b", "", http.StatusOK))
	if poll["handoff"].(map[string]any)["status"] != "not_applied" {
		t.Fatalf("expected orphaned removed-profile handoff unlocked to not_applied, got %+v", poll)
	}
}
