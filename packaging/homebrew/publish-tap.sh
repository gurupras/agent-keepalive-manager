#!/usr/bin/env bash
# publish-tap.sh — commit a rendered formula to the Homebrew tap.
#
#   HOMEBREW_TAP_TOKEN=… packaging/homebrew/publish-tap.sh <formula.rb> <version>
#
# Env: HOMEBREW_TAP_REPO (default gurupras/homebrew-tap), HOMEBREW_TAP_TOKEN
# (a token that can push to that repository), HOMEBREW_TAP_BRANCH (default main).
set -euo pipefail
formula="${1:?usage: publish-tap.sh <formula.rb> <version>}"
version="${2:?usage: publish-tap.sh <formula.rb> <version>}"
repo="${HOMEBREW_TAP_REPO:-gurupras/homebrew-tap}"
branch="${HOMEBREW_TAP_BRANCH:-main}"
: "${HOMEBREW_TAP_TOKEN:?HOMEBREW_TAP_TOKEN is required}"

work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
# The token goes in a header, not the URL, so it never lands in .git/config or logs.
auth="AUTHORIZATION: basic $(printf 'x-access-token:%s' "$HOMEBREW_TAP_TOKEN" | base64 | tr -d '\n')"
git -c http.extraheader="$auth" clone --quiet --depth 1 --branch "$branch" "https://github.com/${repo}.git" "$work/tap"
mkdir -p "$work/tap/Formula"
cp "$formula" "$work/tap/Formula/agent-keepalive.rb"
cd "$work/tap"
if git diff --quiet -- Formula/agent-keepalive.rb && git ls-files --error-unmatch Formula/agent-keepalive.rb >/dev/null 2>&1; then
  echo "[brew] tap already has this formula" >&2; exit 0
fi
git add Formula/agent-keepalive.rb
git -c user.name="${GIT_AUTHOR_NAME:-github-actions[bot]}" \
    -c user.email="${GIT_AUTHOR_EMAIL:-41898282+github-actions[bot]@users.noreply.github.com}" \
    commit --quiet -m "agent-keepalive ${version}"
git -c http.extraheader="$auth" push --quiet origin "HEAD:${branch}"
echo "[brew] pushed agent-keepalive ${version} to ${repo}" >&2
