#!/bin/sh
# Runs existing Go/MyGo fixtures; never starts the daily service or a model.
set -eu
QUEUE_REPO=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$QUEUE_REPO"
QUEUE_RUN="$QUEUE_REPO/.local/regressions/project-queue/$(date -u +%Y%m%dT%H%M%SZ)-$$"
mkdir -p "$QUEUE_RUN"
git rev-parse HEAD > "$QUEUE_RUN/base-commit.txt"
shasum -a 256 internal/simpledesktop/project_queue*.go internal/simpledesktop/settings_redesign.go > "$QUEUE_RUN/source.sha256"
export MEMLANE_OVERVIEW_UI_EVIDENCE="$QUEUE_RUN"
if sh scripts/go.sh test ./internal/simpledesktop -run '^(TestPendingProjectDocumentsUsesOfficialCompletionNotActivityGuess|TestQueuedDocumentReaderDoesNotEnumerateAnotherCatalogAndRestoresScroll|TestProjectQueueActionsShareOneColumn|TestProjectQueueBothTypesAndCompactPermissionFeedback)$' -count=1 -json > "$QUEUE_RUN/test-results.jsonl" 2> "$QUEUE_RUN/stderr.log"
then
    printf 'PASS: project queue regression\nEvidence: %s\n' "$QUEUE_RUN"
else
    cat "$QUEUE_RUN/stderr.log" >&2
    printf 'FAIL: project queue regression\nEvidence: %s\n' "$QUEUE_RUN" >&2
    exit 1
fi
