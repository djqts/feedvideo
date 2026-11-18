#!/bin/sh
# Offline integrity verification only. Actual restore is unavailable in E2.
# Usage: sh restore.sh <backup-dir>; no --apply or implicit backup selection.
set -eu
if [ "$#" -ne 1 ] || [ -z "${1:-}" ] || [ "${1#--}" != "$1" ]; then
  printf 'Usage: %s <backup-dir>; --apply unavailable; data restore NOT_RUN\n' "$0" >&2
  exit 2
fi
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
command -v python3 >/dev/null 2>&1 || { printf 'Integrity: FAIL (python3 required); data restore NOT_RUN\n' >&2; exit 1; }
MSYS2_ENV_CONV_EXCL="FEEDVIDEO_BACKUP_INPUT;${MSYS2_ENV_CONV_EXCL:-}" \
  FEEDVIDEO_BACKUP_INPUT="$1" exec python3 "$SCRIPT_DIR/backup_manifest.py" verify --directory-env
