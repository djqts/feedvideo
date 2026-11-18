#!/usr/bin/env python3
"""Local backup inventory only; never contact a storage service or restore data."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import sys
import tempfile
import uuid

MAX_MANIFEST_BYTES = 32 * 1024 * 1024
MAX_FILES = 100_000
MAX_ENTRIES = 200_000
STORES = {"mysql", "mongo", "minio"}
DUMPS = {"mysql_feedvideo.sql", "mongodb_feedvideo.archive"}
BUCKET_DIRS = {"minio/videos", "minio/covers"}
TOP_FIELDS = {"schema_version", "run_id", "complete", "started_at", "finished_at", "source",
              "source_identity_verified", "consistency", "stores", "files"}


class InvalidBackup(ValueError):
    pass


def require(condition, reason):
    if not condition:
        raise InvalidBackup(reason)


def plain_stat(path):
    info = path.lstat()
    require(not stat.S_ISLNK(info.st_mode) and
            not (getattr(info, "st_file_attributes", 0) & 0x400), "links/reparse points are forbidden")
    return info


def checked_root(value):
    # Check lexical ancestors before resolve, so a supplied junction cannot vanish.
    path = Path(value).absolute()
    for parent in reversed((path, *path.parents)):
        require(stat.S_ISDIR(plain_stat(parent).st_mode), "backup path must be an ordinary directory")
    return path.resolve(strict=True)


def normalized_path(value):
    require(isinstance(value, str) and 0 < len(value) <= 4096, "invalid inventory path")
    require(not any(ord(c) < 32 for c in value) and "\\" not in value and ":" not in value,
            "invalid inventory path")
    parts = value.split("/")
    require(all(p not in ("", ".", "..") for p in parts) and
            not PurePosixPath(value).is_absolute(), "noncanonical inventory path")
    require(value in DUMPS or any(value.startswith(p + "/") for p in BUCKET_DIRS),
            "unexpected inventory path")
    return value


def fingerprint(path):
    before = plain_stat(path)
    require(stat.S_ISREG(before.st_mode), "backup entries must be ordinary files")
    flags = os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0)
    digest = hashlib.sha256()
    size = 0
    with os.fdopen(os.open(path, flags), "rb") as stream:
        opened = os.fstat(stream.fileno())
        require(stat.S_ISREG(opened.st_mode) and (before.st_dev, before.st_ino) ==
                (opened.st_dev, opened.st_ino), "file changed during inventory")
        while chunk := stream.read(1024 * 1024):
            size += len(chunk)
            digest.update(chunk)
        after = os.fstat(stream.fileno())
    current = plain_stat(path)
    # Windows 3.13 lstat ctime may be birth time while fstat ctime is change time.
    signature = lambda s: (s.st_dev, s.st_ino, s.st_size, s.st_mtime_ns)
    require(signature(before) == signature(after) == signature(current) and
            before.st_ctime_ns == current.st_ctime_ns and opened.st_ctime_ns == after.st_ctime_ns and
            size == current.st_size,
            "file changed during inventory")
    return {"size": size, "sha256": digest.hexdigest()}


def inventory(root):
    files, directories = {}, set()
    pending, entries = [root], 0
    while pending:
        directory = pending.pop()
        require(stat.S_ISDIR(plain_stat(directory).st_mode), "invalid inventory directory")
        with os.scandir(directory) as scan:
            for entry in scan:
                entries += 1
                require(entries <= MAX_ENTRIES, "inventory entry limit exceeded")
                path = Path(entry.path)
                relative = path.relative_to(root).as_posix()
                info = plain_stat(path)
                if relative == "manifest.json":
                    require(stat.S_ISREG(info.st_mode), "manifest must be an ordinary file")
                    continue
                if stat.S_ISDIR(info.st_mode):
                    require(relative == "minio" or relative in BUCKET_DIRS or
                            any(relative.startswith(p + "/") for p in BUCKET_DIRS),
                            "unexpected inventory directory")
                    normalized_path(relative + "/placeholder" if relative in BUCKET_DIRS else
                                    relative if relative != "minio" else "minio/videos/placeholder")
                    directories.add(relative)
                    pending.append(path)
                else:
                    normalized_path(relative)
                    require(len(files) < MAX_FILES, "inventory file limit exceeded")
                    files[relative] = fingerprint(path)
    require(BUCKET_DIRS <= directories and DUMPS <= files.keys(), "required backup artifacts missing")
    require(all(files[p]["size"] > 0 for p in DUMPS), "empty required dump")
    return files


def object_fields(value, fields, reason):
    require(isinstance(value, dict) and value.keys() == fields, reason)


def text_field(value):
    require(isinstance(value, str) and 0 < len(value) <= 256 and
            not any(ord(c) < 32 for c in value), "invalid metadata text")


def timestamp(value):
    text_field(value)
    try:
        parsed = datetime.fromisoformat(value)
    except ValueError:
        raise InvalidBackup("invalid metadata timestamp") from None
    require(parsed.utcoffset() is not None and parsed.utcoffset().total_seconds() == 0,
            "timestamp must be UTC")
    return parsed


def validate_metadata(data, complete_required=False):
    object_fields(data, TOP_FIELDS, "invalid manifest fields")
    require(type(data["schema_version"]) is int and data["schema_version"] == 1, "unsupported manifest schema")
    text_field(data["run_id"])
    require(type(data["complete"]) is bool and (data["complete"] or not complete_required),
            "backup is incomplete")
    started = timestamp(data["started_at"])
    if data["finished_at"] is not None:
        require(timestamp(data["finished_at"]) >= started, "invalid finish timestamp")
    require(not data["complete"] or data["finished_at"] is not None, "missing finish timestamp")
    require(data["source_identity_verified"] is False, "source identity proof unsupported")
    object_fields(data["consistency"], {"mode", "verified"}, "invalid consistency fields")
    require(data["consistency"]["mode"] == "uncoordinated" and
            data["consistency"]["verified"] is False, "consistency proof unsupported")
    object_fields(data["source"], STORES, "invalid source fields")
    for store in ("mysql", "mongo"):
        object_fields(data["source"][store], {"container", "database"}, "invalid database source")
        for value in data["source"][store].values():
            text_field(value)
    object_fields(data["source"]["minio"], {"alias", "buckets"}, "invalid object source")
    text_field(data["source"]["minio"]["alias"])
    require(data["source"]["minio"]["buckets"] == ["feedvideo-videos", "feedvideo-covers"],
            "required source buckets missing")
    object_fields(data["stores"], STORES, "required store status missing")
    for value in data["stores"].values():
        object_fields(value, {"status"}, "invalid store status fields")
        require(value["status"] in ("pending", "ok", "error") and
                (not data["complete"] or value["status"] == "ok"), "required store not successful")
    require(isinstance(data["files"], list) and len(data["files"]) <= MAX_FILES, "invalid inventory count")
    seen = set()
    for item in data["files"]:
        object_fields(item, {"path", "size", "sha256"}, "invalid inventory item")
        name = normalized_path(item["path"])
        require(name not in seen, "duplicate inventory path")
        seen.add(name)
        require(type(item["size"]) is int and item["size"] >= 0, "invalid inventory size")
        require(isinstance(item["sha256"], str) and re.fullmatch(r"[0-9a-f]{64}", item["sha256"]),
                "invalid inventory hash")


def unique_fields(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON field")
        result[key] = value
    return result


def read_manifest(root):
    path = root / "manifest.json"
    require(stat.S_ISREG(plain_stat(path).st_mode), "manifest must be an ordinary file")
    with path.open("rb") as stream:
        payload = stream.read(MAX_MANIFEST_BYTES + 1)
    require(len(payload) <= MAX_MANIFEST_BYTES, "manifest size limit exceeded")
    try:
        data = json.loads(payload.decode("utf-8"), object_pairs_hook=unique_fields,
                          parse_constant=lambda _: (_ for _ in ()).throw(InvalidBackup("invalid JSON constant")))
    except (UnicodeError, json.JSONDecodeError, RecursionError):
        raise InvalidBackup("invalid manifest JSON") from None
    validate_metadata(data)
    return data


def write_manifest(root, data):
    validate_metadata(data)
    payload = (json.dumps(data, ensure_ascii=True, indent=2) + "\n").encode("utf-8")
    require(len(payload) <= MAX_MANIFEST_BYTES, "manifest size limit exceeded")
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode="wb", prefix=".manifest-", dir=root, delete=False) as stream:
            temporary = Path(stream.name)
            stream.write(payload)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, root / "manifest.json")
    finally:
        if temporary is not None and temporary.exists():
            temporary.unlink()


def now():
    return datetime.now(timezone.utc).isoformat()


def create(value):
    parent = Path(value).absolute()
    # Inspect each existing prefix before creating any child below it.
    for ancestor in reversed((parent, *parent.parents)):
        try:
            info = plain_stat(ancestor)
        except FileNotFoundError:
            ancestor.mkdir(mode=0o700)
        else:
            require(stat.S_ISDIR(info.st_mode), "backup path must be an ordinary directory")
    parent = checked_root(parent)
    run_id = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + uuid.uuid4().hex
    root = parent / run_id
    root.mkdir(mode=0o700)
    mysql_db = os.environ.get("MYSQL_DATABASE") or "feedvideo"
    data = {"schema_version": 1, "run_id": run_id, "complete": False,
            "started_at": now(), "finished_at": None,
            "source": {
                "mysql": {"container": os.environ.get("MYSQL_CONTAINER") or "feedvideo-mysql", "database": mysql_db},
                "mongo": {"container": os.environ.get("MONGO_CONTAINER") or "feedvideo-mongodb",
                          "database": os.environ.get("MONGO_DATABASE") or mysql_db},
                "minio": {"alias": os.environ.get("MINIO_ALIAS") or "minio",
                          "buckets": ["feedvideo-videos", "feedvideo-covers"]}},
            "source_identity_verified": False, "consistency": {"mode": "uncoordinated", "verified": False},
            "stores": {s: {"status": "pending"} for s in sorted(STORES)}, "files": []}
    write_manifest(root, data)
    print(root.as_posix())


def verify(root):
    data = read_manifest(root)
    validate_metadata(data, complete_required=True)
    actual = inventory(root)
    expected = {item["path"]: {"size": item["size"], "sha256": item["sha256"]} for item in data["files"]}
    require(actual == expected, "inventory size/hash/file mismatch")


def main():
    # Paths returned to POSIX sh must use UTF-8 even with a Windows codepage.
    sys.stdout.reconfigure(encoding="utf-8", errors="strict")
    sys.stderr.reconfigure(encoding="utf-8", errors="replace")
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="operation", required=True)
    for op in ("create", "finalize", "verify", "update"):
        entry = sub.add_parser(op)
        directory = entry.add_mutually_exclusive_group(required=True)
        directory.add_argument("--directory")
        directory.add_argument("--directory-env", action="store_true",
                               help="read the original shell path from FEEDVIDEO_BACKUP_INPUT")
        if op == "update":
            entry.add_argument("store", choices=sorted(STORES))
            entry.add_argument("status", choices=("ok", "error"))
    args = parser.parse_args()
    try:
        value = os.environ.get("FEEDVIDEO_BACKUP_INPUT") if args.directory_env else args.directory
        require(isinstance(value, str) and 0 < len(value) <= 4096 and "\0" not in value,
                "invalid backup directory")
        if os.name == "nt" and value.startswith("/") and not value.startswith("//"):
            drive = re.fullmatch(r"/([A-Za-z])(?:/(.*))?", value)
            require(drive is not None, "Git sh directory must use /drive or a native Windows path")
            value = drive[1].upper() + ":/" + (drive[2] or "")
        if args.operation == "create":
            create(value)
            return 0
        root = checked_root(value)
        if args.operation == "verify":
            verify(root)
            print("Offline integrity: OK; data restore NOT_RUN; consistency and source identity UNVERIFIED")
            return 0
        data = read_manifest(root)
        require(not data["complete"], "completed manifest is immutable")
        if args.operation == "update":
            data["stores"][args.store]["status"] = args.status
        else:
            require(all(s["status"] == "ok" for s in data["stores"].values()), "required store not successful")
            data["files"] = [{"path": p, **v} for p, v in sorted(inventory(root).items())]
            data["finished_at"] = now()
            data["complete"] = True
        write_manifest(root, data)
        return 0
    except InvalidBackup as error:
        print("Backup integrity: FAIL (" + str(error) + ")", file=sys.stderr)
    except (OSError, ValueError, TypeError, OverflowError, RecursionError):
        # Do not echo raw CLI, filesystem contents, or caller-provided metadata.
        print("Backup integrity: FAIL (local file/metadata operation failed)", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
