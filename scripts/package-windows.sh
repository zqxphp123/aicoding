#!/usr/bin/env bash
# 打包 Windows 发行包：dist/记账本-windows-x64.zip
set -euo pipefail
cd "$(dirname "$0")/.."

PKG_NAME="记账本-windows-x64"
WORK="dist/windows"
DEST="${WORK}/${PKG_NAME}"

rm -rf "${WORK}"
mkdir -p "${DEST}/screenshots"

cp "dist/记账本.exe" "${DEST}/记账本.exe"
cp docs/screenshots/overview.png docs/screenshots/project.png docs/screenshots/mobile.png "${DEST}/screenshots/"

# 使用教程：写入 UTF-8 BOM，Windows 记事本打开不乱码
printf '\xEF\xBB\xBF' > "${DEST}/使用教程.md"
cat docs/Windows使用教程.md >> "${DEST}/使用教程.md"

# 启动脚本：纯 ASCII + CRLF，兼容中文/英文 Windows
cat > "${DEST}/start-local.bat" <<'BAT'
@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo Starting AccountBook (local only)...
echo Keep this window open. Close it to quit.
for %%f in (*.exe) do "%%f" -addr 127.0.0.1:8787 -data "%~dp0data"
if errorlevel 1 pause
BAT

cat > "${DEST}/start-phone.bat" <<'BAT'
@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo Starting AccountBook (phone access on the same Wi-Fi)...
echo Keep this window open. Open the LAN URL printed below on your phone.
echo If Windows Firewall asks, allow access on Private networks.
for %%f in (*.exe) do "%%f" -addr 0.0.0.0:8787 -data "%~dp0data"
if errorlevel 1 pause
BAT

crlf() {
  awk '{ sub(/\r$/, ""); printf "%s\r\n", $0 }' "$1" > "$1.tmp" && mv "$1.tmp" "$1"
}
crlf "${DEST}/start-local.bat"
crlf "${DEST}/start-phone.bat"
crlf "${DEST}/使用教程.md"

( cd "${DEST}" && shasum -a 256 "记账本.exe" > SHA256SUMS.txt )

# 用 Python 打包：非 ASCII 文件名会带上 UTF-8 标志位（0x800），
# 否则 Windows 资源管理器会把中文名解成乱码。
rm -f "dist/${PKG_NAME}.zip"
python3 - "${WORK}" "${PKG_NAME}" <<'PY'
import os, sys, zipfile

work, name = sys.argv[1], sys.argv[2]
root = os.path.join(work, name)
out = os.path.join("dist", name + ".zip")

with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames.sort()
        filenames.sort()
        rel = os.path.relpath(dirpath, work)
        if rel != ".":
            info = zipfile.ZipInfo(rel.replace(os.sep, "/") + "/")
            info.external_attr = (0o40755 << 16) | 0x10
            z.writestr(info, b"")
        for fn in filenames:
            path = os.path.join(dirpath, fn)
            arc = os.path.relpath(path, work)
            info = zipfile.ZipInfo.from_file(path, arc)
            info.compress_type = zipfile.ZIP_DEFLATED
            with open(path, "rb") as fh:
                z.writestr(info, fh.read())
PY

echo "完成：dist/${PKG_NAME}.zip"
ls -lh "dist/${PKG_NAME}.zip"
