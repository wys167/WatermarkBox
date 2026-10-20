package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestBundledRuntime 端到端验证内置 runtime：
// findMagick/findGhostscript 指向 bundle 内的二进制，
// 并通过 app 自身的 processImage / renderPDFPage 完成真实加水印。
// 未打包 runtime 时（普通开发机）自动跳过。
func TestBundledRuntime(t *testing.T) {
	bundle, err := filepath.Abs(filepath.Join("build", "bin", "WatermarkBox.app",
		"Contents", "Resources", "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bundle, "bin", magickBinaryName())); err != nil {
		t.Skip("未找到内置 runtime，跳过（先运行 wails build + scripts/bundle-runtime.sh）")
	}

	runtimeRoot = bundle
	defer func() { runtimeRoot = "" }()

	magick, err := findMagick()
	if err != nil {
		t.Fatalf("findMagick: %v", err)
	}
	if filepath.Dir(filepath.Dir(magick)) != filepath.Join(bundle, "bin") &&
		filepath.Dir(magick) != filepath.Join(bundle, "bin") {
		t.Fatalf("magick 未使用内置版本: %s", magick)
	}
	gs, err := findGhostscript()
	if err != nil {
		t.Fatalf("findGhostscript: %v", err)
	}
	if filepath.Dir(gs) != filepath.Join(bundle, "bin") {
		t.Fatalf("gs 未使用内置版本: %s", gs)
	}

	outDir := t.TempDir()
	config := Config{
		Text: "测试水印", FontPath: defaultFontPath(), FontSize: 48,
		Opacity: 0.8, Angle: 0, Color: "#ffffff", Mode: "center",
		OutputFormat: "png", JPEGQuality: 90, StripMetadata: true,
	}

	if err := processImage(context.Background(),
		filepath.Join("test", "t.jpg"),
		filepath.Join(outDir, "watermarked.png"), config); err != nil {
		t.Fatalf("processImage: %v", err)
	}
	if info, err := os.Stat(filepath.Join(outDir, "watermarked.png")); err != nil || info.Size() == 0 {
		t.Fatalf("水印输出无效: %v", err)
	}

	config.Mode = "tile"
	if err := processImage(context.Background(),
		filepath.Join("test", "t.jpg"),
		filepath.Join(outDir, "tiled.png"), config); err != nil {
		t.Fatalf("processImage(tile): %v", err)
	}

	pdfPath := filepath.Join("test", "未命名1.pdf")
	if _, err := os.Stat(pdfPath); err == nil {
		if err := renderPDFPage(context.Background(), pdfPath, 0,
			filepath.Join(outDir, "page.png")); err != nil {
			t.Fatalf("renderPDFPage: %v", err)
		}
	}
}
