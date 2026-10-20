package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCollectJobsHonorsRecursiveSetting(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(root, "top.png"))
	writePNG(t, filepath.Join(nested, "child.png"))
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatal(err)
	}

	jobs, _, err := collectJobs([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("non-recursive jobs = %d, want 1", len(jobs))
	}

	jobs, _, err = collectJobs([]string{root}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("recursive jobs = %d, want 2", len(jobs))
	}
}

func TestValidateConfig(t *testing.T) {
	config := validConfig()
	if err := validateConfig(&config); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	config.Opacity = 0
	if err := validateConfig(&config); err == nil {
		t.Fatal("zero opacity should be rejected")
	}
}

func TestProcessImageCenterAndTile(t *testing.T) {
	if err := checkMagick(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "source.png")
	writePNG(t, source)

	for _, mode := range []string{"center", "tile"} {
		config := validConfig()
		config.Mode = mode
		destination := filepath.Join(root, mode+".png")
		if err := processImage(context.Background(), source, destination, config); err != nil {
			t.Fatalf("%s processing failed: %v", mode, err)
		}
		info, err := os.Stat(destination)
		if err != nil {
			t.Fatalf("%s output missing: %v", mode, err)
		}
		if info.Size() == 0 {
			t.Fatalf("%s output is empty", mode)
		}
	}
}

func TestTiledWatermarkRepeatsCompleteTextByRow(t *testing.T) {
	config := validConfig()
	config.Angle = -30
	config.TileWidth = 80
	config.TileHeight = 180

	repeat := rowRepeatCount(1200, 800, 220, 42, config)
	if repeat < 3 {
		t.Fatalf("row repeats %d complete watermarks, want at least 3", repeat)
	}

	offsets := rowOffsets(1200, 800, 220*repeat, 42, 220, 42, config)
	if len(offsets) < 3 {
		t.Fatalf("generated %d rows, want at least 3", len(offsets))
	}
	// 整行以行为单位倾斜：各条平行行沿垂直于行方向等距分布（允许取整误差1像素）。
	for index := 1; index < len(offsets); index++ {
		dx := offsets[index].X - offsets[index-1].X
		dy := offsets[index].Y - offsets[index-1].Y
		gap := math.Sqrt(float64(dx*dx + dy*dy))
		expected := float64(220*math.Sin(math.Abs(config.Angle)*math.Pi/180)) + float64(42*math.Cos(math.Abs(config.Angle)*math.Pi/180)) + float64(config.TileHeight)
		if math.Abs(gap-expected) > 1.5 {
			t.Fatalf("row %d gap = %.2f, want %.2f: %+v", index, gap, expected, offsets)
		}
		// 相邻行必须沿同一方向等步长移动，即整行整体平移而非各自旋转。
		if index >= 2 {
			prevDx := offsets[index-1].X - offsets[index-2].X
			prevDy := offsets[index-1].Y - offsets[index-2].Y
			if math.Abs(float64(dx-prevDx)) > 1 || math.Abs(float64(dy-prevDy)) > 1 {
				t.Fatalf("row steps are not uniform: %+v", offsets)
			}
		}
	}
}

func TestSingleLineWatermarkTextRemovesUserLineBreaks(t *testing.T) {
	text := singleLineWatermarkText("第一段\n第二段\r\n第三段")
	if text != "第一段 第二段 第三段" {
		t.Fatalf("unexpected single-line watermark: %q", text)
	}
}

func TestSourceRotatesBeforeWatermark(t *testing.T) {
	if err := checkMagick(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "source.png")
	writePNG(t, source)

	for _, test := range []struct {
		rotation   int
		wantWidth  int
		wantHeight int
	}{
		{rotation: -90, wantWidth: 480, wantHeight: 640},
		{rotation: 90, wantWidth: 480, wantHeight: 640},
		{rotation: 180, wantWidth: 640, wantHeight: 480},
	} {
		config := validConfig()
		config.Mode = "center"
		config.RotateOutput = test.rotation
		destination := filepath.Join(root, fmt.Sprintf("rotated-%d.png", test.rotation))
		if err := processImage(context.Background(), source, destination, config); err != nil {
			t.Fatalf("rotation %d failed: %v", test.rotation, err)
		}
		file, err := os.Open(destination)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := png.DecodeConfig(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Width != test.wantWidth || decoded.Height != test.wantHeight {
			t.Fatalf("rotation %d size = %dx%d, want %dx%d", test.rotation, decoded.Width, decoded.Height, test.wantWidth, test.wantHeight)
		}
	}
}

func TestGeneratePreviewForImageAndPDF(t *testing.T) {
	if err := checkMagick(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	imagePath := filepath.Join(root, "preview.png")
	writePNG(t, imagePath)

	app := NewApp()
	preview, err := app.GeneratePreview([]string{imagePath}, validConfig())
	if err != nil {
		t.Fatalf("image preview failed: %v", err)
	}
	if !strings.HasPrefix(preview.DataURL, "data:image/jpeg;base64,") || preview.Width < 1 || preview.Height < 1 {
		t.Fatalf("invalid image preview: %+v", preview)
	}

	if _, err := findGhostscript(); err != nil {
		t.Skip(err)
	}
	pdfPath := filepath.Join(root, "preview.pdf")
	if err := runMagick(context.Background(), imagePath, pdfPath); err != nil {
		t.Fatalf("create PDF: %v", err)
	}
	preview, err = app.GeneratePreview([]string{pdfPath}, validConfig())
	if err != nil {
		t.Fatalf("PDF preview failed: %v", err)
	}
	if !strings.Contains(preview.File, "第1/1页") || preview.Width < 1 || preview.Height < 1 {
		t.Fatalf("invalid PDF preview: %+v", preview)
	}
}

func TestPreviewConfigScalesPixelParameters(t *testing.T) {
	config := validConfig()
	config.FontSize = 155
	config.TileWidth = 80
	config.TileHeight = 280
	scaled := scalePreviewConfig(config, 0.25)
	if scaled.FontSize != 39 || scaled.TileWidth != 20 || scaled.TileHeight != 70 {
		t.Fatalf("unexpected scaled preview config: %+v", scaled)
	}
}

func TestOutputPathDoesNotOverwrite(t *testing.T) {
	job := imageJob{RelativePath: "hotel/license.png"}
	path, err := outputPathFor(job, "png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, outputDirectory) || !strings.HasSuffix(path, "license-已加水印.png") {
		t.Fatalf("unexpected output path: %s", path)
	}
}

func TestPDFCreatesOnePNGJobPerPage(t *testing.T) {
	if err := checkMagick(); err != nil {
		t.Skip(err)
	}
	if _, err := findGhostscript(); err != nil {
		t.Skip(err)
	}

	root := t.TempDir()
	pageOne := filepath.Join(root, "page-one.png")
	pageTwo := filepath.Join(root, "page-two.png")
	pdfPath := filepath.Join(root, "hotel-license.pdf")
	writePNG(t, pageOne)
	writePNG(t, pageTwo)
	if err := runMagick(context.Background(), pageOne, pageTwo, pdfPath); err != nil {
		t.Fatalf("create test PDF: %v", err)
	}

	jobs, _, err := collectJobs([]string{pdfPath}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("PDF jobs = %d, want 2", len(jobs))
	}
	if jobs[0].PDFPage != 0 || jobs[1].PDFPage != 1 || jobs[0].PDFPageCount != 2 {
		t.Fatalf("unexpected PDF jobs: %+v", jobs)
	}

	config := validConfig()
	config.OutputFormat = "jpg"
	for _, job := range jobs {
		output := filepath.Join(root, "rendered-"+strconv.Itoa(job.PDFPage)+".png")
		if err := processJob(context.Background(), job, output, config); err != nil {
			t.Fatalf("process PDF page %d: %v", job.PDFPage+1, err)
		}
		file, err := os.Open(output)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := png.DecodeConfig(file)
		file.Close()
		if err != nil {
			t.Fatalf("output is not PNG: %v", err)
		}
		if decoded.Width != 5333 || decoded.Height != 4000 {
			t.Fatalf("rendered size = %dx%d, want 5333x4000", decoded.Width, decoded.Height)
		}
	}

	path, err := outputPathFor(jobs[0], "jpg")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "hotel-license-第001页-已加水印.png") {
		t.Fatalf("PDF output must be PNG with page number: %s", path)
	}
}

func validConfig() Config {
	return Config{
		Text:          "仅供测试使用",
		FontPath:      defaultFontPath(),
		FontSize:      36,
		Opacity:       0.2,
		Angle:         -30,
		Color:         "#555555",
		Mode:          "tile",
		TileWidth:     400,
		TileHeight:    180,
		OutputFormat:  "png",
		JPEGQuality:   95,
		StripMetadata: true,
		Recursive:     true,
		RotateOutput:  0,
	}
}

func writePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{R: 245, G: 245, B: 240, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}
