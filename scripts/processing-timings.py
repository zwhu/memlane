#!/usr/bin/env python3
"""Summarize local request timings and upstream task spans without content."""
import argparse
from collections import defaultdict, deque
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import statistics


def timestamp(value):
    parsed = datetime.fromisoformat(value.replace('Z', '+00:00'))
    return parsed.replace(tzinfo=timezone.utc) if parsed.tzinfo is None else parsed


def summary(values):
    ordered = sorted(values)
    return {'count': len(values), 'total_ms': round(sum(values), 3),
            'median_ms': round(statistics.median(values), 3),
            'p95_ms': round(ordered[max(0, (len(ordered) * 95 + 99) // 100 - 1)], 3)}


def analyze(paths, since=None):
    samples, outcomes, pending, stages, requests = defaultdict(list), defaultdict(int), defaultdict(deque), defaultdict(list), set()
    for path in paths:
        with path.open(errors='replace') as handle:
            for line in handle:
                if line.startswith('{'):
                    try:
                        row = json.loads(line)
                        if row.get('event') != 'request_finished' or (since and timestamp(row['at']) < since):
                            continue
                        component, identity = row['component'], row['request_id']
                        if component not in ('generation_http', 'codex_bridge', 'generation_validation', 'agent_capture', 'tool_graph', 'incremental_processing') or (component, identity) in requests:
                            continue
                        requests.add((component, identity))
                        label = component + '.' + row.get('host_kind', row.get('backend_kind', 'unknown'))
                        samples[label + '.total'].append(float(row['elapsed_ms']))
                        outcomes[label + '.' + row['outcome']] += 1
                        if row.get('status') is not None:
                            outcomes[label + '.http_' + str(row['status'])] += 1
                        for phase, duration in row['durations_ms'].items():
                            samples[label + '.' + phase].append(float(duration))
                    except (ValueError, TypeError, KeyError):
                        continue
                else:
                    line = re.sub(r'\x1b\[[0-9;]*m', '', line)
                    match = re.search(r'Coroutine task (started|completed): `([a-z_]+)`', line)
                    if not match:
                        continue
                    try:
                        at = timestamp(line[:26])
                    except ValueError:
                        continue
                    if since and at < since:
                        continue
                    action, stage = match.groups()
                    if action == 'started':
                        pending[(path, stage)].append(at)
                    elif pending[(path, stage)]:
                        stages[stage].append((at - pending[(path, stage)].popleft()).total_seconds() * 1000)
    return {'request_phases': {k: summary(v) for k, v in sorted(samples.items())},
            'attempt_outcomes': dict(sorted(outcomes.items())),
            'upstream_stage_spans': {k: summary(v) for k, v in sorted(stages.items())},
            'note': 'HTTP attempts include SDK retries; overlapping and nested span totals are not wall time. Model turn includes provider-side waiting, not pure GPU inference.'}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('logs', nargs='*', type=Path, default=[Path('.local/daily/runtime-logs/official_service_dev.log'),
                                                            Path('.local/daily/runtime-logs/codex_completion_endpoint.log')])
    parser.add_argument('--since', type=timestamp, help='UTC ISO timestamp; no timezone means UTC')
    args = parser.parse_args()
    print(json.dumps(analyze(args.logs, args.since), ensure_ascii=False, indent=2))
