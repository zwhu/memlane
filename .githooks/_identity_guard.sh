#!/bin/sh
# 本仓库提交身份守卫：作者、提交者与标签打标人必须是 zwhu <331946763@qq.com>。
# 用法：
#   _identity_guard.sh current   校验当前将要使用的作者 / 提交者身份（pre-commit、pre-merge-commit）
#   _identity_guard.sh push      从标准输入读取 pre-push 引用列表，校验所有待推送提交与标签（覆盖 cherry-pick / rebase 等不触发 pre-commit 的路径）
set -eu

EXPECTED_NAME="zwhu"
EXPECTED_EMAIL="331946763@qq.com"
ZERO_SHA="0000000000000000000000000000000000000000"

hint() {
    echo "  修复：在本仓库（含独立副本 / Agent 工作区）执行 sh scripts/install-hooks.sh，" >&2
    echo "        或 git config --local user.name $EXPECTED_NAME && git config --local user.email $EXPECTED_EMAIL" >&2
}

ident_ok() {
    # $1 = "Name <email> ts tz"
    name="${1%% <*}"
    rest="${1#*<}"
    email="${rest%%>*}"
    [ "$name" = "$EXPECTED_NAME" ] && [ "$email" = "$EXPECTED_EMAIL" ]
}

check_current() {
    fail=0
    for var in GIT_AUTHOR_IDENT GIT_COMMITTER_IDENT; do
        ident="$(git var "$var")"
        if ! ident_ok "$ident"; then
            echo "identity-guard: $var 为 ${ident%>*}>，本仓库要求 $EXPECTED_NAME <$EXPECTED_EMAIL>" >&2
            fail=1
        fi
    done
    if [ "$fail" -ne 0 ]; then
        hint
        exit 1
    fi
}

check_push() {
    bad="$(mktemp "${TMPDIR:-/tmp}/memlane-identity-guard.XXXXXX")"
    trap 'rm -f "$bad"' EXIT INT TERM
    while read -r local_ref local_sha remote_ref remote_sha; do
        [ -n "${local_sha:-}" ] || continue
        [ "$local_sha" != "$ZERO_SHA" ] || continue
        if [ "$(git cat-file -t "$local_sha")" = "tag" ]; then
            tagger="$(git for-each-ref --format='%(taggername) <%(taggeremail:trim)>' "$local_ref" 2>/dev/null || true)"
            [ -n "$tagger" ] || tagger="$(git cat-file -p "$local_sha" | sed -n 's/^tagger //p' | head -1)"
            if ! ident_ok "$tagger"; then
                echo "$remote_ref tagger: ${tagger%>*}>" >> "$bad"
            fi
        fi
        if [ "$remote_sha" = "$ZERO_SHA" ] || ! git cat-file -e "$remote_sha^{commit}" 2>/dev/null; then
            range="$local_sha --not --remotes"
        else
            range="$remote_sha..$local_sha"
        fi
        # shellcheck disable=SC2086
        git log --format='%h %an <%ae> | %cn <%ce>' $range 2>/dev/null | while read -r line; do
            sha="${line%% *}"
            pair="${line#* }"
            author="${pair%% | *}"
            committer="${pair#* | }"
            if ! ident_ok "$author" || ! ident_ok "$committer"; then
                echo "$sha author=$author committer=$committer" >> "$bad"
            fi
        done
    done
    if [ -s "$bad" ]; then
        echo "identity-guard: 以下待推送对象的身份不是 $EXPECTED_NAME <$EXPECTED_EMAIL>，已拒绝推送：" >&2
        sed 's/^/  /' "$bad" | head -20 >&2
        hint
        echo "  已产生的提交可用 git rebase --exec 'git commit --amend --no-edit --reset-author' <base> 修正" >&2
        exit 1
    fi
}

case "${1:-}" in
    current) check_current ;;
    push) check_push ;;
    *) echo "usage: $0 {current|push}" >&2; exit 2 ;;
esac
