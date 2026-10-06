#!/usr/bin/env bash
# Rebase the fork's custom branch onto sysadminsmedia/homebox main.
# rerere remembers how each conflict was resolved, so repeat conflicts are
# replayed automatically on later runs.
set -euo pipefail

BRANCH="${BRANCH:-custom}"
UPSTREAM="${UPSTREAM:-upstream}"
BASE="${BASE:-main}"

git config rerere.enabled true
git config rerere.autoupdate true

git fetch "${UPSTREAM}" --tags
git fetch origin

# Keep main as a clean mirror of upstream.
git branch -f "${BASE}" "${UPSTREAM}/${BASE}"
git push origin "${BASE}:${BASE}"

git switch "${BRANCH}"
if ! git rebase "${UPSTREAM}/${BASE}"; then
    echo "Rebase stopped. Resolve the conflicts, 'git add' them, run 'git rebase --continue'," >&2
    echo "then push with: git push --force-with-lease origin ${BRANCH}" >&2
    exit 1
fi

git push --force-with-lease origin "${BRANCH}"
