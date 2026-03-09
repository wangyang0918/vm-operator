#!/bin/bash

# fix-commits.sh - Modify commits from source branch and cherry-pick to current branch
# Usage: ./fix-commits.sh <commit-id>
# Example: ./fix-commits.sh main-gitlab 62c8a5f

set -e

# Color definitions
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Target Author info
TARGET_NAME="Yang Wang"
TARGET_EMAIL="danrtsey.wy@alibaba-inc.com"

# Target Co-authored-by info
TARGET_CO_AUTHOR="Copilot <198982749+Copilot@users.noreply.github.com>"

# Temporary branch name
TEMP_BRANCH="__fix-commits-temp__"

# Help message
usage() {
    echo "Usage: $0 <commit-id>"
    echo ""
    echo "Arguments:"
    echo "  source-branch - Source branch name (contains commits to modify)"
    echo "  commit-id     - Starting commit ID (this commit and all following will be modified)"
    echo ""
    echo "Example:"
    echo "  $0 main-gitlab 62c8a5f"
    echo ""
    echo "What it does:"
    echo "  1. Get commits from source branch starting from the specified commit"
    echo "  2. Modify Author to: ${TARGET_NAME} <${TARGET_EMAIL}>"
    echo "  3. Modify Co-authored-by to: ${TARGET_CO_AUTHOR}"
    echo "  4. Cherry-pick modified commits to current branch"
    echo ""
    echo "Note: This operation modifies the current branch. Use 'git push --force' to push to remote."
    exit 1
}

# Cleanup function
cleanup() {
    echo ""
    echo -e "${YELLOW}Cleaning up temporary resources...${NC}"
    git branch -D "$TEMP_BRANCH" 2>/dev/null || true
    git update-ref -d "refs/original/refs/heads/${TEMP_BRANCH}" 2>/dev/null || true
}

# Register cleanup function
trap cleanup EXIT

# Check arguments
if [ $# -ne 2 ]; then
    echo -e "${RED}Error: Incorrect number of arguments${NC}"
    usage
fi

SOURCE_BRANCH="$1"
COMMIT_ID="$2"

# Check if in git repository
if [ ! -d ".git" ]; then
    echo -e "${RED}Error: Current directory is not a git repository${NC}"
    exit 1
fi

# Check if source branch exists
if ! git show-ref --verify --quiet "refs/heads/${SOURCE_BRANCH}"; then
    echo -e "${RED}Error: Source branch '${SOURCE_BRANCH}' does not exist${NC}"
    exit 1
fi

# Check if commit exists on source branch
if ! git merge-base --is-ancestor "${COMMIT_ID}" "${SOURCE_BRANCH}" 2>/dev/null; then
    echo -e "${RED}Error: Commit '${COMMIT_ID}' is not on source branch '${SOURCE_BRANCH}'${NC}"
    exit 1
fi

# Get current branch
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [ "$CURRENT_BRANCH" = "HEAD" ]; then
    CURRENT_BRANCH=$(git rev-parse --short HEAD)
    echo -e "${YELLOW}Warning: Currently in detached HEAD state${NC}"
fi

# Get commit list to modify (from COMMIT_ID to SOURCE_BRANCH HEAD)
COMMIT_LIST=$(git rev-list --reverse "${COMMIT_ID}^..${SOURCE_BRANCH}")
COMMIT_COUNT=$(echo "$COMMIT_LIST" | wc -l | tr -d ' ')

if [ "$COMMIT_COUNT" -eq 0 ]; then
    echo -e "${YELLOW}Warning: No commits found to modify${NC}"
    exit 0
fi

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Git Commit Fix Script${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo "Source branch: ${SOURCE_BRANCH}"
echo "Starting commit: ${COMMIT_ID}"
echo "Current branch: ${CURRENT_BRANCH}"
echo ""
echo "Operations to perform:"
echo "  1. Get ${COMMIT_COUNT} commits from source branch ${SOURCE_BRANCH}"
echo "  2. Modify Author to: ${TARGET_NAME} <${TARGET_EMAIL}>"
echo "  3. Modify Co-authored-by to: ${TARGET_CO_AUTHOR}"
echo "  4. Cherry-pick to current branch ${CURRENT_BRANCH}"
echo ""

echo -e "${YELLOW}Commits to be modified:${NC}"
git log --oneline --reverse "${COMMIT_ID}^..${SOURCE_BRANCH}" | while read -r line; do
    echo -e "  ${YELLOW}${line}${NC}"
done
echo ""

# Confirm operation
read -p "Continue? (y/N): " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo -e "${YELLOW}Operation cancelled${NC}"
    exit 0
fi

echo ""
echo -e "${YELLOW}Step 1: Creating temporary branch...${NC}"
git checkout -b "$TEMP_BRANCH" "$SOURCE_BRANCH"

echo ""
echo -e "${YELLOW}Step 2: Modifying commit Author and Co-authored-by...${NC}"

# Use filter-branch to modify Author and commit message
FILTER_BRANCH_SQUELCH_WARNING=1 git filter-branch -f \
    --env-filter "
export GIT_AUTHOR_NAME=\"${TARGET_NAME}\"
export GIT_AUTHOR_EMAIL=\"${TARGET_EMAIL}\"
export GIT_COMMITTER_NAME=\"${TARGET_NAME}\"
export GIT_COMMITTER_EMAIL=\"${TARGET_EMAIL}\"
" \
    --msg-filter "sed -E 's/Co-authored-by:.*<[^>]+>/Co-authored-by: ${TARGET_CO_AUTHOR}/g'" \
    "${COMMIT_ID}^..HEAD"

echo ""
echo -e "${YELLOW}Step 3: Switching back to ${CURRENT_BRANCH}...${NC}"
git checkout "$CURRENT_BRANCH"

echo ""
echo -e "${YELLOW}Step 4: Cherry-picking modified commits...${NC}"

# Get modified commit list
NEW_COMMIT_LIST=$(git rev-list --reverse "${COMMIT_ID}^..${TEMP_BRANCH}")

# Cherry-pick each commit
CHERRY_PICKED=0
FAILED=0
for commit in $NEW_COMMIT_LIST; do
    echo -e "${YELLOW}Cherry-picking: ${commit}...${NC}"
    if git cherry-pick "$commit" --no-edit; then
        CHERRY_PICKED=$((CHERRY_PICKED + 1))
        echo -e "${GREEN}✓ Success${NC}"
    else
        FAILED=$((FAILED + 1))
        echo -e "${RED}✗ Failed - possible conflict${NC}"
        echo -e "${YELLOW}Resolve conflicts manually, then run: git cherry-pick --continue${NC}"
        echo -e "${YELLOW}Or abort with: git cherry-pick --abort${NC}"
        exit 1
    fi
done

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Done!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo -e "Successfully cherry-picked ${GREEN}${CHERRY_PICKED}${NC} commits to branch ${CURRENT_BRANCH}"
echo ""
echo -e "${YELLOW}Note: To push to remote, use:${NC}"
echo "  git push --force origin ${CURRENT_BRANCH}"
