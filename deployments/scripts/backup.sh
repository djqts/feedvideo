#!/bin/sh
# Required MySQL + MongoDB + both MinIO buckets. Never delete earlier runs.
# Usage: sh backup.sh [backup-root]; relative root is relative to caller cwd.
set -eu
umask 077

if [ "$#" -gt 1 ]; then
  printf 'Usage: %s [backup-root]\n' "$0" >&2
  exit 2
fi
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
DEPLOY_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
HELPER="$SCRIPT_DIR/backup_manifest.py"
BACKUP_ROOT="${1:-$DEPLOY_DIR/backups}"
command -v python3 >/dev/null 2>&1 || { printf 'Backup: FAIL (python3 required)\n' >&2; exit 1; }

# Trusted local deployment configuration; offline verification never loads it.
if [ -f "$DEPLOY_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$DEPLOY_DIR/.env"
  set +a
fi
MYSQL_CONTAINER="${MYSQL_CONTAINER:-feedvideo-mysql}"
MONGO_CONTAINER="${MONGO_CONTAINER:-feedvideo-mongodb}"
MYSQL_DATABASE="${MYSQL_DATABASE:-feedvideo}"
MONGO_DATABASE="${MONGO_DATABASE:-$MYSQL_DATABASE}"
MINIO_ALIAS="${MINIO_ALIAS:-minio}"
export MYSQL_CONTAINER MONGO_CONTAINER MYSQL_DATABASE MONGO_DATABASE MINIO_ALIAS

# Preserve lexical paths across Git sh's automatic Windows path conversion.
manifest() {
  manifest_directory=$1
  shift
  MSYS2_ENV_CONV_EXCL="FEEDVIDEO_BACKUP_INPUT;${MSYS2_ENV_CONV_EXCL:-}" \
    FEEDVIDEO_BACKUP_INPUT="$manifest_directory" python3 "$HELPER" "$@" --directory-env
}
BACKUP_DIR=$(manifest "$BACKUP_ROOT" create)
ACTIVE_STORE=
finish() {
  result=$?
  trap - 0 HUP INT TERM
  if [ "$result" -ne 0 ]; then
    if [ -n "$ACTIVE_STORE" ]; then
      manifest "$BACKUP_DIR" update "$ACTIVE_STORE" error >/dev/null 2>&1 || :
    fi
    printf 'Backup: FAIL; incomplete artifacts retained at %s\n' "$BACKUP_DIR" >&2
  fi
  exit "$result"
}
trap finish 0
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# Missing object tools must fail before reading either database.
ACTIVE_STORE=minio
command -v mc >/dev/null 2>&1 || { printf 'Backup: FAIL (mc required)\n' >&2; exit 1; }
ACTIVE_STORE=mysql
command -v docker >/dev/null 2>&1 || { printf 'Backup: FAIL (docker required)\n' >&2; exit 1; }
: "${MYSQL_PASSWORD:?MYSQL_PASSWORD required}"
: "${MONGO_INITDB_ROOT_USERNAME:?MONGO_INITDB_ROOT_USERNAME required}"
: "${MONGO_INITDB_ROOT_PASSWORD:?MONGO_INITDB_ROOT_PASSWORD required}"

printf '[1/3] Backing up MySQL...\n'
# Keep credentials out of the manifest and diagnostic output.
docker exec --env MYSQL_PWD="$MYSQL_PASSWORD" "$MYSQL_CONTAINER" \
  mysqldump --user=root -- "$MYSQL_DATABASE" > "$BACKUP_DIR/mysql_feedvideo.sql" 2>/dev/null
[ -s "$BACKUP_DIR/mysql_feedvideo.sql" ] || { printf 'Backup: FAIL (empty MySQL dump)\n' >&2; exit 1; }
manifest "$BACKUP_DIR" update mysql ok

ACTIVE_STORE=mongo
printf '[2/3] Backing up MongoDB...\n'
docker exec "$MONGO_CONTAINER" mongodump --host=localhost --port=27017 \
  --username="$MONGO_INITDB_ROOT_USERNAME" --password="$MONGO_INITDB_ROOT_PASSWORD" \
  --authenticationDatabase=admin --db="$MONGO_DATABASE" --archive \
  > "$BACKUP_DIR/mongodb_feedvideo.archive" 2>/dev/null
[ -s "$BACKUP_DIR/mongodb_feedvideo.archive" ] || { printf 'Backup: FAIL (empty MongoDB dump)\n' >&2; exit 1; }
manifest "$BACKUP_DIR" update mongo ok

ACTIVE_STORE=minio
printf '[3/3] Backing up MinIO...\n'
mkdir -p "$BACKUP_DIR/minio/videos" "$BACKUP_DIR/minio/covers"
mc mirror "$MINIO_ALIAS/feedvideo-videos" "$BACKUP_DIR/minio/videos/" >/dev/null 2>&1
mc mirror "$MINIO_ALIAS/feedvideo-covers" "$BACKUP_DIR/minio/covers/" >/dev/null 2>&1
manifest "$BACKUP_DIR" update minio ok

ACTIVE_STORE=
manifest "$BACKUP_DIR" finalize
printf 'Backup artifacts complete: %s\n' "$BACKUP_DIR"
printf 'Consistency and source identity UNVERIFIED; data restore NOT_RUN; old backups retained.\n'
