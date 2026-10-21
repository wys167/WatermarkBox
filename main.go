package main

import (
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// selftest 模式：不启动 GUI，用三种进程上下文各跑一遍平铺管线，排查环境差异。
	if len(os.Args) > 1 && os.Args[1] == "selftest" {
		runSelfTest(os.Args[2:])
		return
	}

	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "图片水印工具",
		Width:     1080,
		Height:    900,
		MinWidth:  880,
		MinHeight: 720,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour:         &options.RGBA{R: 245, G: 247, B: 250, A: 255},
		OnStartup:                app.startup,
		EnableDefaultContextMenu: true,
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
			CSSDropProperty:    "--wails-drop-target",
			CSSDropValue:       "drop",
		},
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: true,
				HideToolbarSeparator:       true,
			},
			Appearance: mac.NSAppearanceNameAqua,
		},
		Bind: []interface{}{app},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
