package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const pdfDensity = 600

var outputDirectory = defaultOutputDirectory()

// runtimeRoot 指向应用内置的 runtime 目录（含 magick/gs 及其依赖）。
// 为空表示未内置，回退到系统 PATH 查找。
var runtimeRoot string

func defaultOutputDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "output"
	}
	return filepath.Join(home, "Desktop", "水印输出")
}

func init() {
	initRuntime()
}

// initRuntime 探测 runtime 目录，优先级：
// 1. 应用旁边的 runtime（便携目录模式）
// 2. Windows 内嵌资源解压到 %LocalAppData% 的副本（单文件模式）
// 3. macOS .app 内的 Resources/runtime
func initRuntime() {
	if exe, err := os.Executable(); err == nil {
		resolved, err := filepath.EvalSymlinks(exe)
		if err == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		if runtime.GOOS == "darwin" {
			dir = filepath.Join(dir, "..", "Resources")
		}
		root := filepath.Join(dir, "runtime")
		if info, err := os.Stat(filepath.Join(root, "bin", magickBinaryName())); err == nil && !info.IsDir() {
			runtimeRoot = root
			return
		}
	}

	if root := prepareEmbeddedRuntime(); root != "" {
		if info, err := os.Stat(filepath.Join(root, "bin", magickBinaryName())); err == nil && !info.IsDir() {
			runtimeRoot = root
		}
	}
}

func magickBinaryName() string {
	if runtime.GOOS == "windows" {
		return "magick.exe"
	}
	return "magick"
}

func ghostscriptBinaryName() string {
	if runtime.GOOS == "windows" {
		return "gswin64c.exe"
	}
	return "gs"
}

var supportedExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	".bmp": true, ".tif": true, ".tiff": true, ".heic": true, ".heif": true,
	".pdf": true,
}

type App struct {
	ctx           context.Context
	mu            sync.Mutex
	running       bool
	cancelRun     context.CancelFunc
	previewCancel context.CancelFunc
	previewSeq    uint64
}

type Config struct {
	Text          string  `json:"text"`
	FontPath      string  `json:"fontPath"`
	FontSize      int     `json:"fontSize"`
	Opacity       float64 `json:"opacity"`
	Angle         float64 `json:"angle"`
	Color         string  `json:"color"`
	Mode          string  `json:"mode"`
	TileWidth     int     `json:"tileWidth"`
	TileHeight    int     `json:"tileHeight"`
	OutputFormat  string  `json:"outputFormat"`
	JPEGQuality   int     `json:"jpegQuality"`
	StripMetadata bool    `json:"stripMetadata"`
	Recursive     bool    `json:"recursive"`
	RotateOutput  int     `json:"rotateOutput"`
}

type InputSummary struct {
	Paths      []string `json:"paths"`
	ImageCount int      `json:"imageCount"`
	Skipped    int      `json:"skipped"`
}

type Progress struct {
	Current int    `json:"current"`
	Total   int    `json:"total"`
	File    string `json:"file"`
	Success int    `json:"success"`
	Failed  int    `json:"failed"`
	Message string `json:"message"`
}

type ProcessResult struct {
	Total           int      `json:"total"`
	Success         int      `json:"success"`
	Failed          int      `json:"failed"`
	OutputDirectory string   `json:"outputDirectory"`
	Errors          []string `json:"errors"`
	Cancelled       bool     `json:"cancelled"`
}

type PreviewResult struct {
	DataURL string `json:"dataURL"`
	File    string `json:"file"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

type imageJob struct {
	Source       string
	RelativePath string
	PDFPage      int
	PDFPageCount int
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	_ = os.MkdirAll(outputDirectory, 0o755)
	wailsruntime.OnFileDrop(ctx, func(_, _ int, paths []string) {
		if len(paths) > 0 {
			wailsruntime.EventsEmit(ctx, "inputs:dropped", paths)
		}
	})
}

func (a *App) GetDefaults() map[string]interface{} {
	return map[string]interface{}{
		"outputDirectory": outputDirectory,
		"fontPath":        defaultFontPath(),
		"magickInstalled": checkMagick() == nil,
	}
}

func configFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(home, ".watermarkbox")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(directory, "config.json"), nil
}

// SaveConfig 把当前水印配置持久化到 ~/.watermarkbox/config.json，下次启动自动恢复。
func (a *App) SaveConfig(config Config) error {
	path, err := configFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadConfig 读取已保存的水印配置；文件不存在时返回零值，由前端回退到默认配置。
func (a *App) LoadConfig() (Config, error) {
	var config Config
	path, err := configFilePath()
	if err != nil {
		return config, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return config, err
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (a *App) SelectFiles() ([]string, error) {
	return wailsruntime.OpenMultipleFilesDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "选择要加水印的图片",
		Filters: []wailsruntime.FileFilter{{
			DisplayName: "图片和PDF文件",
			Pattern:     "*.jpg;*.jpeg;*.png;*.webp;*.bmp;*.tif;*.tiff;*.heic;*.heif;*.pdf",
		}},
	})
}

func (a *App) SelectFolder() ([]string, error) {
	path, err := wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:                "选择包含图片的文件夹",
		CanCreateDirectories: false,
	})
	if err != nil || path == "" {
		return nil, err
	}
	return []string{path}, nil
}

func (a *App) SelectFont() (string, error) {
	path, err := wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "选择字体文件",
		Filters: []wailsruntime.FileFilter{{
			DisplayName: "字体文件",
			Pattern:     "*.ttf;*.ttc;*.otf",
		}},
	})
	return path, err
}

func (a *App) InspectInputs(paths []string, recursive bool) (InputSummary, error) {
	jobs, skipped, err := collectJobs(paths, recursive)
	return InputSummary{Paths: cleanPaths(paths), ImageCount: len(jobs), Skipped: skipped}, err
}

func (a *App) GeneratePreview(paths []string, config Config) (PreviewResult, error) {
	if err := checkMagick(); err != nil {
		return PreviewResult{}, err
	}
	if err := validateConfig(&config); err != nil {
		return PreviewResult{}, err
	}
	jobs, _, err := collectJobs(paths, config.Recursive)
	if err != nil {
		return PreviewResult{}, err
	}
	if len(jobs) == 0 {
		return PreviewResult{}, errors.New("没有找到可预览的图片")
	}

	a.mu.Lock()
	if a.previewCancel != nil {
		a.previewCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.previewSeq++
	previewSeq := a.previewSeq
	a.previewCancel = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		if a.previewSeq == previewSeq {
			a.previewCancel = nil
		}
		a.mu.Unlock()
	}()

	previewSource, scale, cleanup, err := preparePreviewSource(ctx, jobs[0])
	if err != nil {
		return PreviewResult{}, err
	}
	defer cleanup()

	previewConfig := scalePreviewConfig(config, scale)
	previewFile, err := os.CreateTemp("", "watermark-box-preview-*.jpg")
	if err != nil {
		return PreviewResult{}, err
	}
	previewPath := previewFile.Name()
	if err := previewFile.Close(); err != nil {
		return PreviewResult{}, err
	}
	defer os.Remove(previewPath)

	previewConfig.OutputFormat = "jpg"
	previewConfig.JPEGQuality = 82
	previewConfig.StripMetadata = true
	if err := processImage(ctx, previewSource, previewPath, previewConfig); err != nil {
		if ctx.Err() != nil {
			return PreviewResult{}, errors.New("预览已更新")
		}
		return PreviewResult{}, err
	}

	if err := runMagick(ctx, previewPath, "-thumbnail", "1100x780>", "-quality", "82", previewPath); err != nil {
		return PreviewResult{}, err
	}
	data, err := os.ReadFile(previewPath)
	if err != nil {
		return PreviewResult{}, err
	}
	width, height, err := imageDimensions(ctx, previewPath)
	if err != nil {
		return PreviewResult{}, err
	}
	return PreviewResult{
		DataURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data),
		File:    jobDisplayName(jobs[0]),
		Width:   width,
		Height:  height,
	}, nil
}

func (a *App) OpenOutputDirectory() error {
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = newCmd(context.Background(), "open", outputDirectory)
	case "windows":
		cmd = newCmd(context.Background(), "explorer", outputDirectory)
	default:
		cmd = newCmd(context.Background(), "xdg-open", outputDirectory)
	}
	return cmd.Start()
}

func (a *App) CancelProcessing() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelRun != nil {
		a.cancelRun()
	}
}

func (a *App) Process(paths []string, config Config) (ProcessResult, error) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return ProcessResult{}, errors.New("已有处理任务正在运行")
	}
	a.running = true
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelRun = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.running = false
		a.cancelRun = nil
		a.mu.Unlock()
	}()

	if err := checkMagick(); err != nil {
		return ProcessResult{}, err
	}
	if err := validateConfig(&config); err != nil {
		return ProcessResult{}, err
	}
	jobs, _, err := collectJobs(paths, config.Recursive)
	if err != nil {
		return ProcessResult{}, err
	}
	if len(jobs) == 0 {
		return ProcessResult{}, errors.New("没有找到支持的图片文件")
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return ProcessResult{}, fmt.Errorf("创建输出目录失败：%w", err)
	}

	result := ProcessResult{Total: len(jobs), OutputDirectory: outputDirectory}
	for index, job := range jobs {
		if ctx.Err() != nil {
			result.Cancelled = true
			break
		}

		progress := Progress{
			Current: index + 1,
			Total:   len(jobs),
			File:    jobDisplayName(job),
			Success: result.Success,
			Failed:  result.Failed,
			Message: "正在处理",
		}
		wailsruntime.EventsEmit(a.ctx, "process:progress", progress)

		outputPath, pathErr := outputPathFor(job, config.OutputFormat)
		if pathErr == nil {
			pathErr = os.MkdirAll(filepath.Dir(outputPath), 0o755)
		}
		if pathErr == nil {
			pathErr = processJob(ctx, job, outputPath, config)
		}
		if pathErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s：%v", filepath.Base(job.Source), pathErr))
			progress.Message = "处理失败"
		} else {
			result.Success++
			progress.Message = "处理完成"
		}
		progress.Success = result.Success
		progress.Failed = result.Failed
		wailsruntime.EventsEmit(a.ctx, "process:progress", progress)
	}

	return result, nil
}

func collectJobs(paths []string, recursive bool) ([]imageJob, int, error) {
	paths = cleanPaths(paths)
	if len(paths) == 0 {
		return nil, 0, errors.New("请先拖入图片或文件夹")
	}

	var jobs []imageJob
	skipped := 0
	seen := make(map[string]bool)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			skipped++
			continue
		}
		if !info.IsDir() {
			if isSupportedImage(path) && !seen[path] {
				seen[path] = true
				fileJobs, err := jobsForFile(path, filepath.Base(path))
				if err != nil {
					return nil, skipped, err
				}
				jobs = append(jobs, fileJobs...)
			} else {
				skipped++
			}
			continue
		}

		rootName := filepath.Base(filepath.Clean(path))
		walkErr := filepath.WalkDir(path, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				skipped++
				return nil
			}
			if entry.IsDir() {
				if current != path && !recursive {
					return filepath.SkipDir
				}
				return nil
			}
			if !isSupportedImage(current) {
				skipped++
				return nil
			}
			if seen[current] {
				return nil
			}
			seen[current] = true
			relative, err := filepath.Rel(path, current)
			if err != nil {
				return nil
			}
			fileJobs, err := jobsForFile(current, filepath.Join(rootName, relative))
			if err != nil {
				return err
			}
			jobs = append(jobs, fileJobs...)
			return nil
		})
		if walkErr != nil {
			return nil, skipped, walkErr
		}
	}

	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Source == jobs[j].Source {
			return jobs[i].PDFPage < jobs[j].PDFPage
		}
		return jobs[i].Source < jobs[j].Source
	})
	return jobs, skipped, nil
}

func jobsForFile(path, relativePath string) ([]imageJob, error) {
	if !isPDF(path) {
		return []imageJob{{Source: path, RelativePath: relativePath}}, nil
	}
	pageCount, err := pdfPageCount(path)
	if err != nil {
		return nil, fmt.Errorf("读取PDF页数失败（%s）：%w", filepath.Base(path), err)
	}
	jobs := make([]imageJob, 0, pageCount)
	for page := 0; page < pageCount; page++ {
		jobs = append(jobs, imageJob{
			Source:       path,
			RelativePath: relativePath,
			PDFPage:      page,
			PDFPageCount: pageCount,
		})
	}
	return jobs, nil
}

func pdfPageCount(path string) (int, error) {
	ghostscript, err := findGhostscript()
	if err != nil {
		return 0, err
	}
	escapedPath := strings.ReplaceAll(path, "\\", "\\\\")
	escapedPath = strings.ReplaceAll(escapedPath, "(", "\\(")
	escapedPath = strings.ReplaceAll(escapedPath, ")", "\\)")
	command := fmt.Sprintf("(%s) (r) file runpdfbegin pdfpagecount = quit", escapedPath)
	cmd := exec.Command(ghostscript, "-q", "-dNOSAFER", "-dNODISPLAY", "-c", command)
	cmd.Env = commandEnvironment()
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return 0, errors.New(message)
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || count < 1 {
		return 0, errors.New("未识别到有效页面")
	}
	return count, nil
}

func jobDisplayName(job imageJob) string {
	name := filepath.Base(job.Source)
	if job.PDFPageCount > 0 {
		return fmt.Sprintf("%s · 第%d/%d页", name, job.PDFPage+1, job.PDFPageCount)
	}
	return name
}

func preparePreviewSource(ctx context.Context, job imageJob) (string, float64, func(), error) {
	previewFile, err := os.CreateTemp("", "watermark-box-preview-source-*.png")
	if err != nil {
		return "", 1, func() {}, err
	}
	previewPath := previewFile.Name()
	if err := previewFile.Close(); err != nil {
		return "", 1, func() {}, err
	}
	cleanup := func() { _ = os.Remove(previewPath) }

	if job.PDFPageCount > 0 {
		const previewDensity = 150
		if err := renderPDFPageAtDensity(ctx, job.Source, job.PDFPage, previewPath, previewDensity); err != nil {
			cleanup()
			return "", 1, func() {}, err
		}
		return previewPath, float64(previewDensity) / float64(pdfDensity), cleanup, nil
	}

	originalWidth, originalHeight, err := imageDimensions(ctx, job.Source)
	if err != nil {
		cleanup()
		return "", 1, func() {}, err
	}
	if err := runMagick(ctx, job.Source, "-auto-orient", "-thumbnail", "1600x1600>", previewPath); err != nil {
		cleanup()
		return "", 1, func() {}, err
	}
	previewWidth, previewHeight, err := imageDimensions(ctx, previewPath)
	if err != nil {
		cleanup()
		return "", 1, func() {}, err
	}
	scale := math.Min(float64(previewWidth)/float64(originalWidth), float64(previewHeight)/float64(originalHeight))
	if scale <= 0 || scale > 1 {
		scale = 1
	}
	return previewPath, scale, cleanup, nil
}

func scalePreviewConfig(config Config, scale float64) Config {
	if scale <= 0 || scale >= 1 {
		return config
	}
	config.FontSize = max(int(math.Round(float64(config.FontSize)*scale)), 8)
	config.TileWidth = max(int(math.Round(float64(config.TileWidth)*scale)), 20)
	config.TileHeight = max(int(math.Round(float64(config.TileHeight)*scale)), 20)
	return config
}

func imageDimensions(ctx context.Context, path string) (int, int, error) {
	magickPath, err := findMagick()
	if err != nil {
		return 0, 0, err
	}
	cmd := exec.CommandContext(ctx, magickPath, "identify", "-format", "%w %h", path)
	cmd.Env = commandEnvironment()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, 0, errors.New(strings.TrimSpace(string(output)))
	}
	fields := strings.Fields(string(output))
	if len(fields) != 2 {
		return 0, 0, errors.New("无法读取预览尺寸")
	}
	width, widthErr := strconv.Atoi(fields[0])
	height, heightErr := strconv.Atoi(fields[1])
	if widthErr != nil || heightErr != nil {
		return 0, 0, errors.New("无法读取预览尺寸")
	}
	return width, height, nil
}

func processJob(ctx context.Context, job imageJob, destination string, config Config) error {
	if job.PDFPageCount == 0 {
		return processImage(ctx, job.Source, destination, config)
	}

	tempFile, err := os.CreateTemp("", "watermark-box-pdf-page-*.png")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		return err
	}
	defer os.Remove(tempPath)

	if err := renderPDFPage(ctx, job.Source, job.PDFPage, tempPath); err != nil {
		return fmt.Errorf("渲染PDF第%d页失败：%w", job.PDFPage+1, err)
	}
	return processImage(ctx, tempPath, destination, config)
}

func renderPDFPage(ctx context.Context, source string, page int, destination string) error {
	return renderPDFPageAtDensity(ctx, source, page, destination, pdfDensity)
}

func renderPDFPageAtDensity(ctx context.Context, source string, page int, destination string, density int) error {
	ghostscript, err := findGhostscript()
	if err != nil {
		return err
	}
	pageNumber := strconv.Itoa(page + 1)
	cmd := newCmd(ctx, ghostscript,
		"-q", "-dSAFER", "-dBATCH", "-dNOPAUSE",
		"-sDEVICE=png16m", "-r"+strconv.Itoa(density),
		"-dFirstPage="+pageNumber, "-dLastPage="+pageNumber,
		"-sOutputFile="+destination, source,
	)
	cmd.Env = commandEnvironment()
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return errors.New("处理已取消")
	}
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func processImage(ctx context.Context, source, destination string, config Config) error {
	processingSource := source
	if config.RotateOutput != 0 {
		rotatedFile, err := os.CreateTemp("", "watermark-box-rotated-*.miff")
		if err != nil {
			return err
		}
		rotatedPath := rotatedFile.Name()
		if err := rotatedFile.Close(); err != nil {
			return err
		}
		defer os.Remove(rotatedPath)

		if err := runMagick(ctx, source, "-rotate", strconv.Itoa(config.RotateOutput), rotatedPath); err != nil {
			return fmt.Errorf("旋转原图失败：%w", err)
		}
		processingSource = rotatedPath
	}

	if config.Mode == "tile" {
		return processTiledImage(ctx, processingSource, destination, config)
	}

	args := []string{processingSource}
	args = append(args, textArguments(config)...)
	args = append(args, "-gravity", "center", "-annotate", formatAngle(config.Angle), config.Text)
	args = append(args, outputArguments(config)...)
	args = append(args, destination)
	return runMagick(ctx, args...)
}

func processTiledImage(ctx context.Context, source, destination string, config Config) error {
	width, height, err := imageDimensions(ctx, source)
	if err != nil {
		return fmt.Errorf("读取原图尺寸失败：%w", err)
	}

	text := singleLineWatermarkText(config.Text)
	textFile, err := os.CreateTemp("", "watermark-box-text-*.miff")
	if err != nil {
		return err
	}
	textPath := textFile.Name()
	if err := textFile.Close(); err != nil {
		return err
	}
	defer os.Remove(textPath)

	// 先测量单个完整水印内容，用它计算整行需要重复多少份。
	args := []string{"-background", "none"}
	args = append(args, textArguments(config)...)
	args = append(args, "label:"+text, textPath)
	if err := runMagick(ctx, args...); err != nil {
		return fmt.Errorf("生成完整水印文字失败：%w", err)
	}
	textWidth, textHeight, err := imageDimensions(ctx, textPath)
	if err != nil {
		return fmt.Errorf("读取水印文字尺寸失败：%w", err)
	}

	rowFile, err := os.CreateTemp("", "watermark-box-row-*.miff")
	if err != nil {
		return err
	}
	rowPath := rowFile.Name()
	if err := rowFile.Close(); err != nil {
		return err
	}
	defer os.Remove(rowPath)

	// 一行 = 多个完整水印内容水平拼接（间隔为TileWidth），先拼好整行再统一旋转，
	// 这样倾斜以整行为单位，同行文字共用同一条斜向基线，而不是各自单独旋转。
	args = []string{"-background", "none", "-bordercolor", "none"}
	args = append(args, textArguments(config)...)
	repeat := rowRepeatCount(width, height, textWidth, textHeight, config)
	for index := 0; index < repeat; index++ {
		args = append(args, "label:"+text)
	}
	args = append(args,
		"-border", fmt.Sprintf("%dx0", max(config.TileWidth/2, 1)),
		"+append",
		"-rotate", formatAngle(config.Angle),
		"+repage",
		rowPath,
	)
	if err := runMagick(ctx, args...); err != nil {
		return fmt.Errorf("生成整行倾斜水印失败：%w", err)
	}
	rowWidth, rowHeight, err := imageDimensions(ctx, rowPath)
	if err != nil {
		return fmt.Errorf("读取整行水印尺寸失败：%w", err)
	}

	// 把整行沿垂直于行方向逐行铺满原图，行距由TileHeight控制。
	args = []string{source}
	for _, offset := range rowOffsets(width, height, rowWidth, rowHeight, textWidth, textHeight, config) {
		args = append(args, rowPath, "-geometry", fmt.Sprintf("+%d+%d", offset.X, offset.Y), "-compose", "over", "-composite")
	}
	args = append(args, outputArguments(config)...)
	args = append(args, destination)
	return runMagick(ctx, args...)
}

func singleLineWatermarkText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)
	// 以@开头会被ImageMagick当成文件引用（label:@file），前面补空格规避。
	if strings.HasPrefix(text, "@") {
		text = " " + text
	}
	return text
}

// rowRepeatCount 计算一条行需要重复多少份完整水印内容才能覆盖旋转后的画布对角跨度。
func rowRepeatCount(width, height, textWidth, textHeight int, config Config) int {
	angle := config.Angle * math.Pi / 180
	cosA := math.Abs(math.Cos(angle))
	sinA := math.Abs(math.Sin(angle))
	span := float64(width)*cosA + float64(height)*sinA + float64(2*textHeight) + float64(config.TileWidth)
	unit := float64(textWidth + config.TileWidth)
	return max(int(math.Ceil(span/unit))+1, 2)
}

type rowOffset struct {
	X int
	Y int
}

// rowOffsets 计算整行水印的落点：沿垂直于行方向逐行偏移，使平行斜线铺满原图。
func rowOffsets(width, height, rowWidth, rowHeight, textWidth, textHeight int, config Config) []rowOffset {
	angle := config.Angle * math.Pi / 180
	cosA := math.Abs(math.Cos(angle))
	sinA := math.Abs(math.Sin(angle))
	// 行距固定为「文字高度 + TileHeight」，不随倾斜角度变化。
	// 原实现用旋转后行的投影厚度 thickness=textWidth*sinA+textHeight*cosA 参与行距，
	// 而水印文字通常是横向长条（textWidth 远大于 textHeight），thickness 会随 |angle| 增大而显著增大，
	// 把相邻行越推越开，导致「0度最密集、越倾斜越稀疏」。固定行距后密度与角度无关。
	step := float64(textHeight) + float64(config.TileHeight)
	span := float64(width)*sinA + float64(height)*cosA
	count := max(int(math.Ceil(span/step))+2, 2)

	// 行方向单位向量为(cosA, sinA)，其垂直方向为(-sinA, cosA)，保证±90°也正确。
	vx := -math.Sin(angle)
	vy := math.Cos(angle)

	// 相邻行沿行方向错开半个水印单元（砖式错位），避免上下行文字100%对齐。
	hx := math.Cos(angle)
	hy := math.Sin(angle)
	unit := float64(textWidth + config.TileWidth)

	baseX := float64(width)/2 - float64(rowWidth)/2
	baseY := float64(height)/2 - float64(rowHeight)/2
	offsets := make([]rowOffset, 0, count)
	for index := 0; index < count; index++ {
		distance := (float64(index) - float64(count-1)/2) * step
		shift := 0.0
		if index%2 == 1 {
			shift = unit / 2
		}
		offsets = append(offsets, rowOffset{
			X: int(math.Round(baseX + vx*distance + hx*shift)),
			Y: int(math.Round(baseY + vy*distance + hy*shift)),
		})
	}
	return offsets
}

func textArguments(config Config) []string {
	r, g, b := parseHexColor(config.Color)
	alpha := strconv.FormatFloat(config.Opacity, 'f', 3, 64)
	return []string{
		"-font", config.FontPath,
		"-pointsize", strconv.Itoa(config.FontSize),
		"-fill", fmt.Sprintf("rgba(%d,%d,%d,%s)", r, g, b, alpha),
	}
}

func outputArguments(config Config) []string {
	args := make([]string, 0, 6)
	if config.StripMetadata {
		args = append(args, "-strip")
	}
	if config.OutputFormat == "jpg" {
		args = append(args, "-background", "white", "-alpha", "remove", "-alpha", "off", "-quality", strconv.Itoa(config.JPEGQuality))
	}
	return args
}

// newCmd 创建外部命令，Windows 下隐藏控制台窗口，避免 GUI 应用闪黑框。
func newCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hideConsole(cmd)
	return cmd
}

func runMagick(ctx context.Context, args ...string) error {
	path, err := findMagick()
	if err != nil {
		return err
	}
	cmd := newCmd(ctx, path, args...)
	cmd.Env = commandEnvironment()
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return errors.New("处理已取消")
	}
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func outputPathFor(job imageJob, format string) (string, error) {
	extension := ".png"
	if job.PDFPageCount == 0 {
		if format == "jpg" {
			extension = ".jpg"
		} else if format == "webp" {
			extension = ".webp"
		}
	}
	directory := filepath.Join(outputDirectory, filepath.Dir(job.RelativePath))
	base := strings.TrimSuffix(filepath.Base(job.RelativePath), filepath.Ext(job.RelativePath))
	if job.PDFPageCount > 0 {
		base = fmt.Sprintf("%s-第%03d页", base, job.PDFPage+1)
	}
	candidate := filepath.Join(directory, base+"-已加水印"+extension)
	for index := 2; ; index++ {
		_, err := os.Stat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = filepath.Join(directory, fmt.Sprintf("%s-已加水印-%d%s", base, index, extension))
	}
}

func validateConfig(config *Config) error {
	config.Text = strings.TrimSpace(config.Text)
	if config.Text == "" {
		return errors.New("水印文字不能为空")
	}
	if strings.TrimSpace(config.FontPath) == "" {
		config.FontPath = defaultFontPath()
	}
	if _, err := os.Stat(config.FontPath); err != nil {
		return fmt.Errorf("字体文件不可用：%s", config.FontPath)
	}
	if config.FontSize < 8 || config.FontSize > 400 {
		return errors.New("字号需要在8到400之间")
	}
	if config.Opacity < 0.01 || config.Opacity > 1 {
		return errors.New("透明度需要在0.01到1之间")
	}
	if config.Angle < -180 || config.Angle > 180 {
		return errors.New("角度需要在-180到180之间")
	}
	if config.Mode != "tile" && config.Mode != "center" {
		return errors.New("水印模式不正确")
	}
	if config.TileWidth < 20 || config.TileWidth > 3000 || config.TileHeight < 20 || config.TileHeight > 2000 {
		return errors.New("平铺尺寸超出允许范围")
	}
	if config.OutputFormat != "png" && config.OutputFormat != "jpg" && config.OutputFormat != "webp" {
		return errors.New("输出格式不正确")
	}
	if config.JPEGQuality < 1 || config.JPEGQuality > 100 {
		return errors.New("JPEG质量需要在1到100之间")
	}
	if config.RotateOutput < -270 || config.RotateOutput > 270 || config.RotateOutput%90 != 0 {
		return errors.New("输出旋转必须是向左或向右90°的整数倍，最多3次")
	}
	if _, _, _, ok := parseHexColorStrict(config.Color); !ok {
		return errors.New("颜色格式不正确")
	}
	return nil
}

func checkMagick() error {
	_, err := findMagick()
	return err
}

func commandEnvironment() []string {
	environment := os.Environ()
	pathValue := os.Getenv("PATH")
	if runtimeRoot != "" {
		runtimeBin := filepath.Join(runtimeRoot, "bin")
		if !strings.Contains(":"+pathValue+":", ":"+runtimeBin+":") {
			pathValue = runtimeBin + ":" + pathValue
		}
	}
	for _, directory := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"} {
		if !strings.Contains(":"+pathValue+":", ":"+directory+":") {
			pathValue = directory + ":" + pathValue
		}
	}
	if runtimeRoot != "" {
		separator := ":"
		if runtime.GOOS == "windows" {
			separator = ";"
		}
		environment = append(environment, "MAGICK_HOME="+runtimeRoot)
		// macOS 版 ImageMagick 以模块形式编译 coder/filter，需显式告知路径；
		// Windows 便携版为静态内置，目录不存在时不能设置（设了反而会屏蔽内置注册）
		codersPath := filepath.Join(runtimeRoot, "lib", "ImageMagick", "modules-Q16HDRI", "coders")
		filtersPath := filepath.Join(runtimeRoot, "lib", "ImageMagick", "modules-Q16HDRI", "filters")
		if info, err := os.Stat(codersPath); err == nil && info.IsDir() {
			environment = append(environment, "MAGICK_CODER_MODULE_PATH="+codersPath)
		}
		if info, err := os.Stat(filtersPath); err == nil && info.IsDir() {
			environment = append(environment, "MAGICK_FILTER_MODULE_PATH="+filtersPath)
		}
		configPaths := []string{
			filepath.Join(runtimeRoot, "lib", "ImageMagick", "config-Q16HDRI"),
			filepath.Join(runtimeRoot, "etc", "ImageMagick-7"),
		}
		if info, err := os.Stat(configPaths[0]); err == nil && info.IsDir() {
			environment = append(environment, "MAGICK_CONFIGURE_PATH="+strings.Join(configPaths, separator))
		}
		if info, err := os.Stat(filepath.Join(runtimeRoot, "gs", "Resource", "Init")); err == nil && info.IsDir() {
			environment = append(environment, "GS_LIB="+gsLibraryPath())
		}
	}
	for index, item := range environment {
		if strings.HasPrefix(item, "PATH=") {
			environment[index] = "PATH=" + pathValue
			return environment
		}
	}
	return append(environment, "PATH="+pathValue)
}

// gsLibraryPath 返回内置 Ghostscript 的资源搜索路径（GS_LIB）。
func gsLibraryPath() string {
	separator := ":"
	if runtime.GOOS == "windows" {
		separator = ";"
	}
	parts := []string{
		filepath.Join(runtimeRoot, "gs", "Resource", "Init"),
		filepath.Join(runtimeRoot, "gs", "Resource", "Font"),
		filepath.Join(runtimeRoot, "gs", "Resource", "CIDFont"),
		filepath.Join(runtimeRoot, "gs", "Resource", "CIDFSubst"),
		filepath.Join(runtimeRoot, "gs", "lib"),
		filepath.Join(runtimeRoot, "gs", "fonts"),
	}
	return strings.Join(parts, separator)
}

func findMagick() (string, error) {
	if runtimeRoot != "" {
		path := filepath.Join(runtimeRoot, "bin", magickBinaryName())
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	if path, err := exec.LookPath("magick"); err == nil && path != "" {
		return path, nil
	}

	// 从Finder启动的macOS应用不会继承终端的Homebrew PATH。
	candidates := []string{
		"/opt/homebrew/bin/magick",
		"/usr/local/bin/magick",
		"/usr/bin/magick",
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("未找到ImageMagick，请先安装并确认 magick -version 可运行")
}

func cleanPaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	seen := make(map[string]bool)
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" || seen[path] {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	return result
}

func isSupportedImage(path string) bool {
	return supportedExtensions[strings.ToLower(filepath.Ext(path))]
}

func isPDF(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".pdf")
}

func findGhostscript() (string, error) {
	if runtimeRoot != "" {
		path := filepath.Join(runtimeRoot, "bin", ghostscriptBinaryName())
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	if path, err := exec.LookPath("gs"); err == nil && path != "" {
		return path, nil
	}
	candidates := []string{
		"/opt/homebrew/bin/gs",
		"/usr/local/bin/gs",
		"/usr/bin/gs",
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", errors.New("PDF处理需要Ghostscript，请先执行 brew install ghostscript")
}

func defaultFontPath() string {
	candidates := []string{
		"/System/Library/Fonts/Hiragino Sans GB.ttc",
		"/System/Library/Fonts/STHeiti Medium.ttc",
		"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
		"C:\\Windows\\Fonts\\msyh.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return candidates[0]
}

func parseHexColor(value string) (int, int, int) {
	r, g, b, _ := parseHexColorStrict(value)
	return r, g, b
}

func parseHexColorStrict(value string) (int, int, int, bool) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(value) != 6 {
		return 0, 0, 0, false
	}
	parsed, err := strconv.ParseUint(value, 16, 24)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(parsed >> 16), int((parsed >> 8) & 0xff), int(parsed & 0xff), true
}

func formatAngle(angle float64) string {
	if math.Mod(angle, 1) == 0 {
		return strconv.Itoa(int(angle))
	}
	return strconv.FormatFloat(angle, 'f', 2, 64)
}
