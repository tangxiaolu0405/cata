package main

import (
	"embed"
	"log"

	"cata/cmd/cata-pet/pet"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := pet.NewApp()
	s := pet.LoadSettings()

	err := wails.Run(&options.App{
		Title:         "Cata Pet",
		Width:         180,
		Height:        200,
		MinWidth:      160,
		MinHeight:     180,
		Frameless:     true,
		AlwaysOnTop:   s.AlwaysOnTop,
		DisableResize: false,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 0},
		OnStartup:        app.Startup,
		OnDomReady:       app.DomReady,
		Bind:             []interface{}{app},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHidden(),
			// WebviewIsTransparent + CSS rgba(0,0,0,0) = clear pet.
			// WindowIsTranslucent adds macOS vibrancy (looks gray) — keep off.
			WebviewIsTransparent: true,
			WindowIsTranslucent:  false,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			DisableWindowIcon:    true,
		},
		Linux: &linux.Options{
			WindowIsTranslucent: true,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
