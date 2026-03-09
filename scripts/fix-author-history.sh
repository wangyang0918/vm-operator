#!/bin/bash
# fix-author-history.sh
#
# Creates a new branch (default: fix-author-history) from main, rewrites all
# commit authors to danrtsey.wy@alibaba-inc.com, and appends a
# Co-authored-by: Copilot trailer to every commit message.
#
# Usage:
#   ./scripts/fix-author-history.sh [TARGET_BRANCH] [SOURCE_BRANCH]
#
# Examples:
#   ./scripts/fix-author-history.sh                        # fix-author-history from main
#   ./scripts/fix-author-history.sh fix-author-history main

set -euo pipefail

TARGET_BRANCH="${1:-fix-author-history}"
SOURCE_BRANCH="${2:-main}"

TARGET_EMAIL="danrtsey.wy@alibaba-inc.com"
TARGET_NAME="Yang Wang"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MSG_FILTER="$SCRIPT_DIR/rewrite-commit-msg.sh"
chmod +x "$MSG_FILTER"

echo "==> Fetching '$SOURCE_BRANCH' with full history..."
if [ "$(git rev-parse --is-shallow-repository)" = "true" ]; then
    git fetch --unshallow origin "$SOURCE_BRANCH"
else
    git fetch origin "$SOURCE_BRANCH"
fi

echo "==> Creating branch '$TARGET_BRANCH' from 'origin/$SOURCE_BRANCH'..."
if git show-ref --verify --quiet "refs/heads/$TARGET_BRANCH"; then
    echo "    Branch '$TARGET_BRANCH' already exists locally; deleting and recreating."
    git branch -D "$TARGET_BRANCH"
fi
git checkout -b "$TARGET_BRANCH" "origin/$SOURCE_BRANCH"

echo "==> Rewriting commit history (author + Co-authored-by trailer)..."
FILTER_BRANCH_SQUELCH_WARNING=1 git filter-branch -f \
    --env-filter "
        export GIT_AUTHOR_NAME='$TARGET_NAME'
        export GIT_AUTHOR_EMAIL='$TARGET_EMAIL'
        export GIT_COMMITTER_NAME='$TARGET_NAME'
        export GIT_COMMITTER_EMAIL='$TARGET_EMAIL'
    " \
    --msg-filter "$MSG_FILTER" \
    HEAD

echo "==> Pushing '$TARGET_BRANCH' to origin..."
if git ls-remote --exit-code --heads origin "$TARGET_BRANCH" > /dev/null 2>&1; then
    git push origin "$TARGET_BRANCH" --force-with-lease
else
    git push -u origin "$TARGET_BRANCH"
fi

echo ""
echo "✅ Done! Branch '$TARGET_BRANCH' has been pushed to origin."
echo "   All commits now have:"
echo "     Author:  $TARGET_NAME <$TARGET_EMAIL>"
echo "     Trailer: Co-authored-by: Copilot <198982749+Copilot@users.noreply.github.com>"
