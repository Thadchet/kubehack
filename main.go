package main

import (
	"embed"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

// version is set at build time by run.sh from the latest git tag.
var version = "dev"

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// macOS routes Cmd+C and Cmd+V through the Edit menu, so without one copy and
	// paste do nothing in the editor or the terminal. Windows needs no menu.
	var appMenu *menu.Menu
	if runtime.GOOS == "darwin" {
		appMenu = menu.NewMenu()
		appMenu.Append(menu.AppMenu())
		appMenu.Append(menu.EditMenu())
		appMenu.Append(menu.WindowMenu())
	}

	// Create application with options
	err := wails.Run(&options.App{
		Title:  "kubehack",
		Menu:   appMenu,
		Width:  1280,
		Height: 800,
		// Below this the sidebar and the dock crowd the resource table.
		MinWidth:  900,
		MinHeight: 560,
		// Wails turns the zoom (green) button off when Mac is nil, so it must be
		// set even though every field keeps its default.
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title:   "Kubehack " + version,
				Message: "A Kubernetes desktop app.\ngithub.com/Thadchet/kubehack",
				Icon:    icon,
			},
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
