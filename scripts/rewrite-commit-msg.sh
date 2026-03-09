#!/bin/sh
# Helper script used by git filter-branch --msg-filter
# Reads commit message from stdin, appends Co-authored-by trailer if not present.

COAUTHOR="Co-authored-by: Copilot <198982749+Copilot@users.noreply.github.com>"

msg=$(cat)
stripped=$(printf '%s' "$msg" | sed 's/[[:space:]]*$//')

if printf '%s' "$stripped" | grep -qF "$COAUTHOR"; then
    printf '%s\n' "$stripped"
else
    printf '%s\n\n%s\n' "$stripped" "$COAUTHOR"
fi
