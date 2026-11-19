"""CLI entry for all supported local Agent bridges; no provider credentials."""
import argparse
from pathlib import Path
from types import SimpleNamespace

from aiohttp import web
from codex_completion_endpoint import create_app
from generation_bridge import check_bridge_runtime


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--port', type=int, required=True)
    parser.add_argument('--token-file', type=Path, required=True)
    parser.add_argument('--agent-executable', type=Path, required=True)
    parser.add_argument('--auth-home', type=Path, required=True)
    parser.add_argument('--bridge-kind', choices=('codex', 'pi', 'traex', 'antigravity'), required=True)
    parser.add_argument('--bridge-provider')
    parser.add_argument('--model', required=True)
    args = parser.parse_args()
    profile = {'bridge_kind': args.bridge_kind}
    check_bridge_runtime({'agent_hosts': {args.bridge_kind: {'executable': str(args.agent_executable),
                         'profile': str(args.auth_home)}}}, profile)
    args.codex_executable, args.timeout = args.agent_executable, 90
    if args.bridge_kind == 'pi':
        from pi_completion import execute
        args.execute_request = execute
    elif args.bridge_kind == 'antigravity':
        from antigravity_completion import execute
        args.execute_request = execute
    web.run_app(create_app(args), host='127.0.0.1', port=args.port, access_log=None, print=None,
                handler_cancellation=True)


if __name__ == '__main__':
    main()
