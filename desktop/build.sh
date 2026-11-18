#!/usr/bin/env bash
# Build the desktop client with the toolchain Qt was compiled against.
#
#   bash desktop/build.sh          # configure + build
#   bash desktop/build.sh run      # configure + build, then run
#
# Qt ships its own MinGW (13.1) and that is the ABI the Qt libraries were built
# with; the system MinGW on this machine is 15.2 and must not be used.
set -euo pipefail

QT_ROOT="${QT_ROOT:-D:/Qt/6.9.1/mingw_64}"
QT_MINGW="${QT_MINGW:-D:/Qt/Tools/mingw1310_64}"
QT_NINJA="${QT_NINJA:-D:/Qt/Tools/Ninja/ninja.exe}"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here"

if [ ! -x "$QT_NINJA" ]; then
  echo "ninja not found at $QT_NINJA (set QT_NINJA)" >&2
  exit 1
fi

export PATH="$QT_ROOT/bin:$QT_MINGW/bin:$(dirname "$QT_NINJA"):$PATH"

cmake -S . -B build -G Ninja \
  -DCMAKE_MAKE_PROGRAM="$QT_NINJA" \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_PREFIX_PATH="$QT_ROOT" \
  -DCMAKE_C_COMPILER="$QT_MINGW/bin/gcc.exe" \
  -DCMAKE_CXX_COMPILER="$QT_MINGW/bin/g++.exe" \
  | tail -3

"$QT_NINJA" -C build

if [ "${1:-}" = "run" ]; then
  # Live QML: edit qml/*.qml and restart, no rebuild needed.
  export FEEDVIDEO_QML="$here/qml"
  "./build/feedvideo.exe"
fi
