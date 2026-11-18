#!/usr/bin/env bash
# Produce a runnable folder next to the build (DLLs, QML plugins, fonts).
#
#   bash desktop/package.sh            -> desktop/dist/FeedVideo/
#
set -euo pipefail

QT_ROOT="${QT_ROOT:-D:/Qt/6.9.1/mingw_64}"
QT_MINGW="${QT_MINGW:-D:/Qt/Tools/mingw1310_64}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here"

export PATH="$QT_ROOT/bin:$QT_MINGW/bin:$PATH"

if [ ! -f build/feedvideo.exe ]; then
  echo "build/feedvideo.exe missing — run desktop/build.sh first" >&2
  exit 1
fi

rm -rf dist/FeedVideo
mkdir -p dist/FeedVideo
cp build/feedvideo.exe dist/FeedVideo/

"$QT_ROOT/bin/windeployqt.exe" \
  --release \
  --qmldir "$here/qml" \
  --no-translations \
  --no-system-d3d-compiler \
  dist/FeedVideo/feedvideo.exe > /dev/null

# The multimedia backend needs its FFmpeg DLLs next to the binary.
"$QT_ROOT/bin/windeployqt.exe" --release --no-translations dist/FeedVideo/feedvideo.exe > /dev/null

echo "packaged to desktop/dist/FeedVideo"
du -sh dist/FeedVideo
