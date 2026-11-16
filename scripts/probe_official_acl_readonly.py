#!/usr/bin/env python3
"""Read existing daily ACLs using GET only; never provision, grant, or write data."""
import json
from pathlib import Path
import sys
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def main():
    runtime = json.loads((ROOT / '.local/daily/runtime.json').read_text())
    registry = json.loads(Path(runtime['project_control']['registry_file']).read_text())
    origin = runtime['service_url'].rstrip('/')
    parsed = urllib.parse.urlparse(origin)
    if parsed.scheme != 'http' or parsed.hostname != '127.0.0.1':
        raise SystemExit('expected existing loopback daily service')
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs): return None
    opener = urllib.request.build_opener(NoRedirect)
    requests = 0
    def get(path, key_file):
        nonlocal requests
        requests += 1
        request = urllib.request.Request(origin + path, method='GET', headers={
            'X-Api-Key': Path(key_file).read_text().strip()})
        with opener.open(request, timeout=10) as response:
            return json.load(response)
    owner = get('/api/v1/users/me', runtime['token_file'])
    sys.path.insert(0, str(ROOT / 'engine/python'))
    from project_policy import effective
    results = []
    for ident, agent in registry['agents'].items():
        if agent.get('kind') not in ('codex', 'traex'): continue
        me = get('/api/v1/users/me', agent['key_file'])
        independent = me['id'] == agent['principal_id'] and me.get('parent_user_id') == owner['id'] and me['id'] != owner['id']
        principals = {me['id']}
        tenants = get('/api/v1/permissions/tenants/me', agent['key_file'])
        for tenant in tenants:
            principals.add(tenant['id'])
            roles = get('/api/v1/permissions/tenants/' + tenant['id'] + '/roles/users/' + me['id'], runtime['token_file'])
            principals.update(role['id'] for role in roles)
        grants = {}
        for permission in ('read', 'write', 'delete', 'share'):
            grants[permission] = set()
            for principal in principals:
                rows = get('/api/v1/permissions/principals/' + principal + '/datasets?permission_name=' + permission, agent['key_file'])
                grants[permission].update(row['id'] for row in rows)
        checked = mismatches = 0
        for project in registry['projects'].values():
            if project.get('status') != 'verified': continue
            expected = set(effective(registry['defaults'], project.get('overrides', {}).get(ident, {}))['permissions'])
            actual = {p for p, ids in grants.items() if project['dataset_id'] in ids}
            checked += 1
            mismatches += actual != expected
        results.append({'kind': agent['kind'], 'independent_child': independent,
                        'verified_projects_checked': checked, 'policy_acl_mismatches': mismatches,
                        'share_grants': len(grants['share'])})
    print(json.dumps({'scope': 'existing daily service; GET only; no dataset contents',
                      'http_get_requests': requests, 'results': results}, indent=2))
    return 0 if results and all(r['independent_child'] and r['verified_projects_checked'] and
                               r['policy_acl_mismatches'] == 0 and r['share_grants'] == 0 for r in results) else 1


if __name__ == '__main__':
    raise SystemExit(main())
