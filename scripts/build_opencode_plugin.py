#!/usr/bin/env python3
"""Build the pinned managed OpenCode dev component from verified offline caches."""
import argparse
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'engine/python'))
from opencode_installation import build

if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', required=True, help='new artifact directory')
    parser.add_argument('--npm-cache', required=True, help='cache populated from the fixed upstream package lock')
    parser.add_argument('--node', default='node', help='Node executable used for compilation')
    args = parser.parse_args()
    print(json.dumps(build(args.output, args.npm_cache, node=args.node), sort_keys=True))
