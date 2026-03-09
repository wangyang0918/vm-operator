#!/usr/bin/env bash
# setup-git.sh — Configure local git settings for vm-operator contributions.
#
# Run this script once after cloning the repository:
#   bash scripts/setup-git.sh
#
# It sets:
#   - commit.template  -> .gitmessage (adds Co-authored-by trailer automatically)
#   - user.email       -> danrtsey.wy@alibaba-inc.com
#   - A commit-msg hook that ensures the Co-authored-by trailer is present

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
HOOKS_DIR="$REPO_ROOT/.git/hooks"
TEMPLATE="$REPO_ROOT/.gitmessage"

AUTHOR_EMAIL="danrtsey.wy@alibaba-inc.com"
CO_AUTHOR_TRAILER="Co-authored-by: Copilot <198982749+Copilot@users.noreply.github.com>"

# 1. Set commit author email
git config user.email "$AUTHOR_EMAIL"
echo "✓ user.email set to $AUTHOR_EMAIL"

# 2. Set commit message template
git config commit.template "$TEMPLATE"
echo "✓ commit.template set to .gitmessage"

# 3. Install commit-msg hook
cat > "$HOOKS_DIR/commit-msg" << HOOK
#!/usr/bin/env bash
# commit-msg hook: ensure Co-authored-by trailer is present in every commit.
COMMIT_MSG_FILE="\$1"
TRAILER="${CO_AUTHOR_TRAILER}"

if ! grep -qF "\$TRAILER" "\$COMMIT_MSG_FILE"; then
  # Append after the last non-blank, non-comment line
  echo "" >> "\$COMMIT_MSG_FILE"
  echo "\$TRAILER" >> "\$COMMIT_MSG_FILE"
fi
HOOK
chmod +x "$HOOKS_DIR/commit-msg"
echo "✓ commit-msg hook installed"

echo ""
echo "All done. Future commits in this repository will automatically include:"
echo "  Author:          $AUTHOR_EMAIL"
echo "  $CO_AUTHOR_TRAILER"
