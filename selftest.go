package main

// selftest：排查 Windows 上“命令成功但水印不显示”的专用模式。
// 用真实配置把平铺管线以三种进程上下文各跑一遍，输出三张图到桌面：
//
//	A = 应用原生方式（commandEnvironment + 隐藏控制台）
//	B = 继承进程默认环境（无 MAGICK 变量，仍隐藏控制台）
//	C = 纯 exec.Command（默认环境 + 可见控制台）
//
// 若 A 无水印而 B/C 有，则毒药在 commandEnvironment 注入的变量；
// 若 A/B 无水印而 C 有，则问题出在 CREATE_NO_WINDOW 隐藏控制台标志。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func runSelfTest(args []string) {
	ctx := context.Background()

	source := ""
	if len(args) > 0 {
		source = args[0]
	}
	if source == "" {
		for _, candidate := range []string{
			`E:\Pictures\联想截图\联想截图_20261006221243.png`,
			`C:\Users\11440\Desktop\selftest-input.png`,
		} {
			if _, err := os.Stat(candidate); err == nil {
				source = candidate
				break
			}
		}
	}

	home, _ := os.UserHomeDir()
	report := filepath.Join(home, "Desktop", "selftest-report.txt")
	if source == "" {
		os.WriteFile(report, []byte("找不到测试图片，请把图片路径作为第二个参数传入\n"), 0o644)
		return
	}

	// 与用户 Windows 机器上保存的 config.json 完全一致。
	config := Config{
		Text:          "仅供共比邻小程序酒店资质展示使用 严禁他用",
		FontPath:      `C:\Windows\Fonts\msyh.ttc`,
		FontSize:      155,
		Opacity:       0.22,
		Angle:         -30,
		Color:         "#FB2828",
		Mode:          "tile",
		TileWidth:     80,
		TileHeight:    280,
		OutputFormat:  "png",
		JPEGQuality:   95,
		StripMetadata: true,
	}

	reportText := fmt.Sprintf("source: %s\ntime: %s\n\n", source, time.Now().Format(time.RFC3339))

	// 完整 argv、中间 miff、尺寸与落点全部落盘，供远程排查。
	magickArgLogLimit = 100000
	keepTempFiles = true
	tiledDebugf = func(format string, args ...interface{}) {
		reportText += fmt.Sprintf("  [diag] "+format+"\n", args...)
	}

	type variant struct {
		name   string
		config Config
		run    func()
	}
	variants := []variant{
		{"A-原生", config, func() {}},
		{"B-默认环境", config, func() { magickInheritEnv = true }},
		{"C-纯exec", config, func() { magickPlainExec = true }},
		{"D-纯exec-ASCII", func() Config { c := config; c.Text = "WATERMARK TEST 123"; return c }(), func() { magickPlainExec = true }},
	}

	for _, v := range variants {
		magickInheritEnv = false
		magickPlainExec = false
		v.run()
		destination := filepath.Join(home, "Desktop", "selftest-"+v.name+".png")
		start := time.Now()
		err := processTiledImage(ctx, source, destination, v.config)
		reportText += fmt.Sprintf("%s: err=%v 耗时=%s 文件=%s\n", v.name, err, time.Since(start).Round(time.Millisecond), destination)
		if data, statErr := os.Stat(destination); statErr == nil {
			reportText += fmt.Sprintf("  大小=%d 字节\n", data.Size())
		}
	}
	magickInheritEnv = false
	magickPlainExec = false
	tiledDebugf = nil

	os.WriteFile(report, []byte(reportText), 0o644)
}
