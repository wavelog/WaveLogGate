package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"waveloggate/internal/debug"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "-debug" {
			debug.Verbose = true
			break
		}
	}

	if debug.Verbose {
		f, err := os.OpenFile("waveloggate-debug.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err == nil {
			log.SetOutput(f)
			defer f.Close()
		}
	}

	app := NewApp()

	err := wails.Run(&options.App{
		Title:            "WavelogGate2 by DJ7NT " + appVersion,
		Width:            430,
		Height:           620,
		MinWidth:         430,
		MinHeight:        130,
		DisableResize:    false,
		BackgroundColour: &options.RGBA{R: 48, G: 48, B: 48, A: 255},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
