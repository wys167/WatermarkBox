#!/bin/bash
# 将 ImageMagick / Ghostscript 及其全部动态库依赖打包进 WatermarkBox.app，
# 使应用在任何 macOS (arm64) 机器上无需安装 Homebrew 即可使用。
#
# 用法：先执行 wails build，再运行本脚本。
# 兼容 macOS 自带 bash 3.2（不使用关联数组 / 管道子shell内改状态）。
set -euo pipefail

SRC_DIR="$(cd "$(dirname "$0")/.." && pwd)"
APP="$SRC_DIR/build/bin/WatermarkBox.app"
RUNTIME="$APP/Contents/Resources/runtime"
LIB_DIR="$RUNTIME/lib"
BIN_DIR="$RUNTIME/bin"
GS_DIR="$RUNTIME/gs"

MAGICK="$(which magick)"
GS="$(which gs)"
IM_PREFIX="$(brew --prefix imagemagick 2>/dev/null || echo "$(dirname "$(dirname "$MAGICK")")")"

log() { echo "[bundle] $*"; }

# ---------------------------------------------------------------- dylib 收集
# 解析 @rpath/xxx 依赖的真实路径：优先在当前文件同目录找，再按其 LC_RPATH 逐个尝试
resolve_dep() {
    local dep="$1"
    local current="$2"
    local name
    name="$(basename "$dep")"
    local candidates=()
    candidates+=("$(dirname "$current")/$name")
    local rp
    while IFS= read -r rp; do
        [ -z "$rp" ] && continue
        rp="${rp//@loader_path/$(dirname "$current")}"
        case "$rp" in @executable_path*) continue ;; esac
        candidates+=("$rp/$name")
    done < <(otool -l "$current" | awk '/LC_RPATH/{getline; print $2}')
    local c
    for c in "${candidates[@]}"; do
        if [ -f "$c" ]; then
            echo "$c"
            return 0
        fi
    done
    return 1
}

# 递归收集二进制/dylib 的非系统依赖，拷贝到 $LIB_DIR（按文件名去重）。
# 关键：队列里保存"原始路径"，@rpath/@loader_path 依赖按原位置解析
# （拷贝件里的 rpath 在 bundle 里解析不出来，例如 libwebp 的 @loader_path/../lib）。
collect_deps() {
    local queue=("$1")
    local head=0
    local current dep name real
    while [ "$head" -lt "${#queue[@]}" ]; do
        current="${queue[$head]}"
        head=$((head + 1))
        for dep in $(otool -L "$current" | tail -n +2 | awk '{print $1}'); do
            case "$dep" in
                /usr/lib/*|/System/*) continue ;;
                @rpath/*|@loader_path/*)
                    real="$(resolve_dep "$dep" "$current")" || {
                        echo "[bundle] 警告: 无法解析依赖 $dep (来自 $current)" >&2
                        continue
                    }
                    ;;
                @*) continue ;;
                *) real="$dep" ;;
            esac
            name="$(basename "$real")"
            if [ ! -f "$LIB_DIR/$name" ]; then
                cp -L "$real" "$LIB_DIR/$name"
                queue+=("$real")
            fi
        done
    done
}

# 将文件的依赖路径重写为 $prefix/<name>（$prefix 基于 @loader_path，相对各文件自身位置）
# 用法: rewrite_deps <file> <lib前缀>
rewrite_deps() {
    local file="$1"
    local prefix="$2"
    local dep
    for dep in $(otool -L "$file" | tail -n +2 | awk '{print $1}'); do
        case "$dep" in
            /usr/lib/*|/System/*) continue ;;
            @rpath/*|@loader_path/*)
                install_name_tool -change "$dep" "$prefix$(basename "$dep")" "$file"
                ;;
            @*) ;;
            *)
                install_name_tool -change "$dep" "$prefix$(basename "$dep")" "$file"
                ;;
        esac
    done
    case "$file" in
        *.dylib)
            install_name_tool -id "$prefix$(basename "$file")" "$file"
            ;;
    esac
}

# ---------------------------------------------------------------- 开始
[ -d "$APP" ] || { echo "未找到 $APP，请先执行 wails build"; exit 1; }

rm -rf "$RUNTIME"
mkdir -p "$LIB_DIR" "$BIN_DIR" "$GS_DIR"

log "拷贝 magick / gs ..."
cp "$MAGICK" "$BIN_DIR/magick"
cp "$GS" "$BIN_DIR/gs"

log "递归收集动态库依赖 ..."
collect_deps "$MAGICK"
collect_deps "$GS"

log "收集 coder/filter 模块依赖的动态库 ..."
for mod in "$IM_PREFIX/lib/ImageMagick/modules-Q16HDRI/coders/"*.so \
           "$IM_PREFIX/lib/ImageMagick/modules-Q16HDRI/filters/"*.so; do
    [ -f "$mod" ] && collect_deps "$mod"
done

log "拷贝 ImageMagick 配置与模块: $IM_PREFIX ..."
cp -R "$IM_PREFIX/lib/ImageMagick" "$LIB_DIR/ImageMagick"
# MAGICK_HOME 约定：etc/ 与 share/ 位于其根目录下
mkdir -p "$RUNTIME/etc" "$RUNTIME/share"
cp -R "$IM_PREFIX/etc/ImageMagick-7" "$RUNTIME/etc/ImageMagick-7"
cp -R "$IM_PREFIX/share/ImageMagick-7" "$RUNTIME/share/ImageMagick-7" 2>/dev/null || true

# .la 文件内嵌 Homebrew 绝对路径，装了 ImageMagick 的机器上会加载
# 系统模块并与 bundle 内的 MagickCore 冲突（no decode delegate），
# 改写为 bundle 内部路径
log "改写模块 .la 路径 ..."
find "$LIB_DIR/ImageMagick/modules-Q16HDRI" -name "*.la" | while read -r la; do
    sed -i '' "s|^libdir='.*'|libdir='$(dirname "$la")'|" "$la"
done

log "拷贝 Ghostscript 资源 ..."
GS_SHARE="$(dirname "$GS")/../share/ghostscript"
cp -R "$GS_SHARE/Resource" "$GS_DIR/Resource"
cp -R "$GS_SHARE/lib" "$GS_DIR/lib"
cp -R "$GS_SHARE/fonts" "$GS_DIR/fonts"
cp -R "$GS_SHARE/iccprofiles" "$GS_DIR/iccprofiles"

log "重写动态库加载路径 ..."
# bin/ 下二进制的库在 ../lib；lib/ 下 dylib 的库在同目录；模块 .so 在 lib 下三级
rewrite_deps "$BIN_DIR/magick" "@loader_path/../lib/"
rewrite_deps "$BIN_DIR/gs" "@loader_path/../lib/"
for dylib in "$LIB_DIR"/*.dylib; do
    rewrite_deps "$dylib" "@loader_path/"
done
find "$LIB_DIR/ImageMagick" \( -name "*.dylib" -o -name "*.so" \) | while read -r mod; do
    rewrite_deps "$mod" "@loader_path/../../../"
done

# 注意：codesign --deep 不会重签 Resources 下嵌套的 Mach-O（仅作为资源封存），
# install_name_tool 改过的二进制会因签名失效被内核 SIGKILL，必须逐个重签。
log "逐个重签 runtime 内的 Mach-O ..."
find "$RUNTIME" -type f \( -name "*.dylib" -o -name "*.so" -o -name "magick" -o -name "gs" \) | while read -r f; do
    codesign --force --sign - "$f"
done

log "重新签名应用 ..."
codesign --force --sign - "$APP"

log "完成。runtime 大小："
du -sh "$RUNTIME"
