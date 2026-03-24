package main

import (
	"embed"

	"net"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	startupURL := ""
	if len(os.Args) > 1 {
		for _, arg := range os.Args[1:] {
			if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
				startupURL = arg
				break
			}
		}
	}

	// 单实例检测：尝试监听固定端口
	const port = "54321"
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		// 监听失败，说明已有实例运行。尝试发送 URL 并退出。
		if startupURL != "" {
			conn, sendErr := net.Dial("tcp", "127.0.0.1:"+port)
			if sendErr == nil {
				conn.Write([]byte(startupURL))
				conn.Close()
			}
		}
		return
	}
	defer ln.Close()

	app := NewApp()
	app.StartupURL = startupURL
	app.listener = ln // 保存监听器供 app.go 使用

	// Create application with options
	err = wails.Run(&options.App{
		Title:  "my-browser",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
