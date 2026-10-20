# 图片水印工具

基于 Go、Wails 和 ImageMagick 的 macOS 本地批量水印工具。

## 功能

- 拖入单张、多张图片或整个文件夹
- 支持 JPG、JPEG、PNG、WebP、BMP、TIFF、HEIC、HEIF
- 支持递归处理子文件夹并保留目录结构
- 动态设置文字、字体、字号、颜色、透明度、角度、平铺间距和输出格式
- 支持满屏平铺或居中单水印
- 可删除 EXIF、GPS 等元数据
- 所有结果统一输出到 `/Users/Admin/Desktop/ImageMagick/output`
- 不覆盖原图，重名结果自动加序号

## 环境要求

开发环境：

- Go 1.25+
- Node.js 18+
- Wails v2
- ImageMagick 7（`brew install imagemagick`）
- Ghostscript（`brew install ghostscript`，处理 PDF 需要）

最终用户**无需安装任何依赖**——ImageMagick / Ghostscript 及其全部运行库已打包进应用内。

## 开发运行

```bash
$(go env GOPATH)/bin/wails dev
```

## 构建 macOS 应用（自带全部依赖）

```bash
$(go env GOPATH)/bin/wails build -platform darwin/arm64
./scripts/bundle-runtime.sh
```

构建结果位于 `build/bin/WatermarkBox.app`。

## 构建 Windows 应用（在 macOS 上交叉编译）

```bash
$(go env GOPATH)/bin/wails build -platform windows/amd64
```

Windows 侧的 runtime（静态版 magick.exe + gswin64c + VC 运行时）组装方式见
`scripts/` 与分发目录 `../dist/WatermarkBox-windows-x64.zip` 的结构
（`WatermarkBox.exe` 旁放 `runtime/bin`、`runtime/gs`）。

## 分发包

位于 `../dist/`：

- `WatermarkBox-macOS-arm64.zip` — macOS Apple Silicon，解压即用
- `WatermarkBox-windows-x64.zip` — Windows x64，解压即用

macOS 首次打开若被 Gatekeeper 拦截（未公证签名），右键点应用选"打开"即可。
输出目录为 `~/Desktop/水印输出`。
