package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/cryptoadvance/specter-virtual-host/internal/cli"
	"github.com/cryptoadvance/specter-virtual-host/internal/control"
	"github.com/cryptoadvance/specter-virtual-host/internal/core"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

const applicationName = "Specter Virtual Host"

var version = "dev"

//go:embed all:frontend/dist
var frontendAssets embed.FS

//go:embed build/appicon.png
var applicationIcon []byte

func main() {
	arguments := os.Args[1:]
	if len(arguments) == 0 {
		prepareGUIConsole()
		if err := runGUI(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if arguments[0] == "serve" || arguments[0] == "--headless" || arguments[0] == "--site" {
		if err := runHeadless(arguments); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(cli.ExitFailure)
		}
		return
	}
	runner := cli.Runner{Version: version, Stdout: os.Stdout, Stderr: os.Stderr}
	os.Exit(runner.Run(context.Background(), arguments))
}

func runHeadless(arguments []string) error {
	legacySiteInvocation := len(arguments) > 0 && arguments[0] == "--site"
	if len(arguments) > 0 && (arguments[0] == "serve" || arguments[0] == "--headless") {
		arguments = arguments[1:]
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	headless := flags.Bool("headless", true, "run without a graphical interface")
	backgroundChild := flags.Bool("background-child", false, "internal background process")
	noOpen := flags.Bool("no-open", false, "do not open the simulator for the legacy --site startup form")
	site := flags.String("site", "", "simulator site URL")
	configPath := flags.String("config", "", "configuration file path")
	webAddress := flags.String("web-address", envOr("SPECTER_VIRTUAL_HOST_WEB_ADDRESS", "127.0.0.1:8788"), "browser bridge listen address")
	hwiAddress := flags.String("hwi-address", envOr("SPECTER_VIRTUAL_HOST_HWI_ADDRESS", "127.0.0.1:8789"), "wallet/HWI listen address")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if !*headless {
		return errors.New("serve is always headless; omit --headless or set it to true")
	}
	run := func(stop <-chan struct{}, ready chan<- error) error {
		instance, err := core.NewWithAddresses(version, "headless", *configPath, *webAddress, *hwiAddress)
		if err != nil {
			ready <- err
			return err
		}
		defer instance.Close()
		if *site != "" {
			if _, err := instance.Execute(context.Background(), "settings.set", map[string]string{"key": "site", "value": *site}); err != nil {
				ready <- err
				return err
			}
		}
		server, err := control.StartServer(instance)
		if err != nil {
			ready <- err
			return err
		}
		defer server.Close()
		if err := instance.StartBridge(); err != nil {
			ready <- err
			return err
		}
		if legacySiteInvocation && !*noOpen {
			_, _ = instance.Execute(context.Background(), "simulator.open", nil)
		}
		ready <- nil
		<-stop
		return nil
	}
	if handled, err := runWindowsService(run); handled {
		return err
	}

	stop := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	ready := make(chan error, 1)
	done := make(chan error, 1)
	go func() { done <- run(stop, ready) }()
	if err := <-ready; err != nil {
		return err
	}
	if !*backgroundChild {
		fmt.Fprintf(os.Stdout, "%s %s\n", applicationName, version)
		fmt.Fprintln(os.Stdout, "Headless bridge running")
		fmt.Fprintf(os.Stdout, "Browser endpoint: http://%s/connected\n", *webAddress)
		fmt.Fprintf(os.Stdout, "HWI endpoint:     %s\n", *hwiAddress)
	}
	select {
	case <-signals:
		close(stop)
		return <-done
	case err := <-done:
		return err
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func runGUI() error {
	backend := newApplication(version)
	return wails.Run(&options.App{
		Title:                    applicationName,
		Width:                    1320,
		Height:                   850,
		MinWidth:                 980,
		MinHeight:                680,
		Frameless:                false,
		DisableResize:            false,
		BackgroundColour:         &options.RGBA{R: 4, G: 13, B: 21, A: 255},
		AssetServer:              &assetserver.Options{Assets: frontendAssets},
		Linux:                    &linux.Options{Icon: applicationIcon, ProgramName: "Specter Virtual Host"},
		OnStartup:                backend.startup,
		OnShutdown:               backend.shutdown,
		Bind:                     []interface{}{backend},
		EnableDefaultContextMenu: false,
	})
}
