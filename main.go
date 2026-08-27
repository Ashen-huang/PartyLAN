package main

import (
	"embed"

	"PartyLAN/internal/appmode"
	"PartyLAN/internal/network"
	"PartyLAN/pkg/diaglog"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if _, err := diaglog.Setup(); err != nil {
		println("Warning:", err.Error())
	}

	net, err := network.NewNetworkForMode(appmode.ResolveMode())
	if err != nil {
		println("Error:", err.Error())
		return
	}
	app := NewApp(net)

	err = wails.Run(&options.App{
		Title:     "partylan",
		Width:     1120,
		Height:    630,
		Frameless: true,
		MinWidth:  920,
		MinHeight: 560,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
