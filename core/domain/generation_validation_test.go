package domain

import (
	"context"
	"testing"
)

func TestGetAndListGenerationValidations(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.GetGenerationValidation(ctx, "")
	wantError(t, err, ErrNotFound)
	_, err = s.GetGenerationValidation(ctx, "missing-op")
	wantError(t, err, ErrNotFound)

	first := GenerationValidation{
		OperationID:        "op-1",
		ProfileID:          "prov-1",
		ProfileRevision:    2,
		Digest:             "digest-a",
		RegisteredRevision: 1,
		Status:             "passed",
		PlainPassed:        true,
		SchemaPassed:       true,
		QualityPassed:      true,
		StructuredMode:     "json_schema",
		ErrorCode:          "",
		PayloadJSON:        `{"agent_defaults":{"model":"gpt-5.4","provider":"openai"}}`,
		UpdatedAt:          "2026-11-27T12:00:00.000000+00:00",
	}
	second := GenerationValidation{
		OperationID:        "op-2",
		ProfileID:          "prov-2",
		ProfileRevision:    1,
		Digest:             "digest-b",
		RegisteredRevision: 3,
		Status:             "failed",
		ErrorCode:          "authentication_failed",
		PayloadJSON:        `{}`,
		UpdatedAt:          "2026-11-27T12:01:00.000000+00:00",
	}
	must(t, s.RecordGenerationValidation(ctx, first))
	must(t, s.RecordGenerationValidation(ctx, second))

	got, err := s.GetGenerationValidation(ctx, "op-1")
	must(t, err)
	if got != first {
		t.Fatalf("GetGenerationValidation(op-1) = %+v, want %+v", got, first)
	}
	got2, err := s.GetGenerationValidation(ctx, "op-2")
	must(t, err)
	if got2 != second {
		t.Fatalf("GetGenerationValidation(op-2) = %+v, want %+v", got2, second)
	}
	_, err = s.GetGenerationValidation(ctx, "op-3")
	wantError(t, err, ErrNotFound)

	list, err := s.ListGenerationValidations(ctx)
	must(t, err)
	if len(list) != 2 {
		t.Fatalf("ListGenerationValidations len = %d, want 2", len(list))
	}
}

func TestRecordGenerationValidationAutoRevisionPreservesPassed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 1. Unvalidated digest: consecutive interrupted runs increment MAX+1 (avoid 0==0 tie).
	c1, err := s.RecordGenerationValidationAutoRevision(ctx, GenerationValidation{
		OperationID: "unval-cancel-1", ProfileID: "p-new", ProfileRevision: 1,
		Digest: "digest-new", Status: "failed", ErrorCode: "validation_interrupted",
	}, true)
	must(t, err)
	if c1.RegisteredRevision != 1 {
		t.Fatalf("first cancel on unvalidated digest got rev=%d, want 1", c1.RegisteredRevision)
	}
	c2, err := s.RecordGenerationValidationAutoRevision(ctx, GenerationValidation{
		OperationID: "unval-cancel-2", ProfileID: "p-new", ProfileRevision: 1,
		Digest: "digest-new", Status: "failed", ErrorCode: "validation_interrupted",
	}, true)
	must(t, err)
	if c2.RegisteredRevision != 2 {
		t.Fatalf("second cancel on unvalidated digest got rev=%d, want 2", c2.RegisteredRevision)
	}
	valid, err := s.IsGenerationDigestValidated(ctx, "digest-new")
	must(t, err)
	if valid {
		t.Fatal("cancelled-only digest reported as validated")
	}

	// 2. Digest with existing passed (rev=1): interrupted revalidations write rev=0 and keep passed intact.
	passed, err := s.RecordGenerationValidationAutoRevision(ctx, GenerationValidation{
		OperationID: "pass-1", ProfileID: "p-ok", ProfileRevision: 1,
		Digest: "digest-ok", Status: "passed", PlainPassed: true, SchemaPassed: true, QualityPassed: true,
		StructuredMode: "json_schema",
	}, false)
	must(t, err)
	if passed.RegisteredRevision != 1 {
		t.Fatalf("initial passed rev=%d, want 1", passed.RegisteredRevision)
	}
	valid, err = s.IsGenerationDigestValidated(ctx, "digest-ok")
	must(t, err)
	if !valid {
		t.Fatal("passed digest not reported as validated")
	}

	for _, tc := range []struct {
		op, status, code string
	}{
		{"cancel-reval-1", "failed", "validation_interrupted"},
		{"cancel-reval-2", "failed", "validation_interrupted"},
		{"owner-interrupt-1", "result_unknown", "validation_owner_interrupted"},
	} {
		rec, err := s.RecordGenerationValidationAutoRevision(ctx, GenerationValidation{
			OperationID: tc.op, ProfileID: "p-ok", ProfileRevision: 1,
			Digest: "digest-ok", Status: tc.status, ErrorCode: tc.code,
		}, true)
		must(t, err)
		if rec.RegisteredRevision != 0 {
			t.Fatalf("%s on passed digest got rev=%d, want 0", tc.op, rec.RegisteredRevision)
		}
		valid, err = s.IsGenerationDigestValidated(ctx, "digest-ok")
		must(t, err)
		if !valid {
			t.Fatalf("%s revoked existing passed validation", tc.op)
		}
	}

	// 3. Real failure or timeout (preservePassed=false) advances MAX+1 and revokes prior passed.
	timeoutRec, err := s.RecordGenerationValidationAutoRevision(ctx, GenerationValidation{
		OperationID: "timeout-reval", ProfileID: "p-ok", ProfileRevision: 1,
		Digest: "digest-ok", Status: "result_unknown", ErrorCode: "validation_timeout",
	}, false)
	must(t, err)
	if timeoutRec.RegisteredRevision != 2 {
		t.Fatalf("timeout revalidation rev=%d, want 2", timeoutRec.RegisteredRevision)
	}
	valid, err = s.IsGenerationDigestValidated(ctx, "digest-ok")
	must(t, err)
	if valid {
		t.Fatal("real timeout did not revoke previous passed validation")
	}

	// 4. After revocation, a subsequent cancel increments normally (MAX+1 = 3).
	afterRevoke, err := s.RecordGenerationValidationAutoRevision(ctx, GenerationValidation{
		OperationID: "cancel-after-revoke", ProfileID: "p-ok", ProfileRevision: 1,
		Digest: "digest-ok", Status: "failed", ErrorCode: "validation_interrupted",
	}, true)
	must(t, err)
	if afterRevoke.RegisteredRevision != 3 {
		t.Fatalf("cancel after real failure got rev=%d, want 3", afterRevoke.RegisteredRevision)
	}
	valid, err = s.IsGenerationDigestValidated(ctx, "digest-ok")
	must(t, err)
	if valid {
		t.Fatal("cancelled validation after failure reported as validated")
	}
}
