package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/cryptoadvance/specter-virtual-host/internal/control"
	"github.com/cryptoadvance/specter-virtual-host/internal/core"
	"github.com/cryptoadvance/specter-virtual-host/internal/model"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const connectionRequestCategory = "specter-connection-request"

type Application struct {
	version            string
	ctx                context.Context
	executor           core.Executor
	owned              *core.Core
	server             *control.Server
	notificationsReady bool
	startupErr         error
	bridgeErrMu        sync.RWMutex
	bridgeStartupErr   error
}

func newApplication(appVersion string) *Application {
	return &Application{version: appVersion}
}

func (a *Application) initialize() error {
	if control.Available(context.Background()) {
		a.executor = control.NewClient()
		return nil
	}
	instance, err := core.New(a.version, "gui", "")
	if err != nil {
		return err
	}
	server, err := control.StartServer(instance)
	if err != nil {
		_ = instance.Close()
		if control.Available(context.Background()) {
			a.executor = control.NewClient()
			return nil
		}
		return err
	}
	settings := instance.Snapshot().Settings
	bridgeStarted := false
	if settings.StartBridgeOnLaunch {
		if err := instance.StartBridge(); err != nil {
			a.setBridgeStartupError(err)
		} else {
			bridgeStarted = true
		}
	}
	if settings.OpenSimulatorOnLaunch && bridgeStarted {
		_, _ = instance.Execute(context.Background(), "simulator.open", nil)
	}
	a.executor, a.owned, a.server = instance, instance, server
	return nil
}

func (a *Application) startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.initialize(); err != nil {
		a.startupErr = err
		return
	}
	if err := runtime.InitializeNotifications(ctx); err != nil {
		return
	}
	if err := runtime.RegisterNotificationCategory(ctx, runtime.NotificationCategory{
		ID: connectionRequestCategory,
		Actions: []runtime.NotificationAction{
			{ID: "allow-once", Title: "Allow Once"},
			{ID: "trust", Title: "Allow Permanently"},
		},
	}); err != nil {
		return
	}
	runtime.OnNotificationResponse(ctx, a.handleNotificationResponse)
	a.notificationsReady = true
}

func (a *Application) NotifyConnectionRequest(request model.PendingRequest) error {
	if a.ctx == nil || !a.notificationsReady {
		return errors.New("desktop notifications are not ready")
	}
	if !runtime.IsNotificationAvailable(a.ctx) {
		return errors.New("desktop notifications are unavailable")
	}
	authorized, err := runtime.CheckNotificationAuthorization(a.ctx)
	if err != nil {
		return err
	}
	if !authorized {
		authorized, err = runtime.RequestNotificationAuthorization(a.ctx)
		if err != nil {
			return err
		}
		if !authorized {
			return errors.New("desktop notifications are not authorized")
		}
	}
	return runtime.SendNotificationWithActions(a.ctx, runtime.NotificationOptions{
		ID:         "specter-connection-" + request.ID,
		Title:      "Specter Virtual Host — connection request",
		Body:       fmt.Sprintf("%s wants to connect to your local Specter Virtual Host.", request.Origin),
		CategoryID: connectionRequestCategory,
		Data:       map[string]interface{}{"requestId": request.ID},
	})
}

func (a *Application) handleNotificationResponse(result runtime.NotificationResult) {
	if result.Error != nil {
		return
	}
	response := result.Response
	requestID, _ := response.UserInfo["requestId"].(string)
	if requestID == "" {
		requestID = strings.TrimPrefix(response.ID, "specter-connection-")
	}
	if response.ActionIdentifier == "DEFAULT_ACTION" {
		if a.ctx != nil {
			runtime.WindowShow(a.ctx)
			runtime.WindowUnminimise(a.ctx)
		}
		return
	}
	command := ""
	switch response.ActionIdentifier {
	case "allow-once":
		command = "requests.allow-once"
	case "trust":
		command = "requests.trust"
	case "deny":
		command = "requests.deny"
	}
	if command == "" || requestID == "" {
		return
	}
	if a.executor == nil {
		return
	}
	if _, err := a.executor.Execute(context.Background(), command, map[string]string{"id": requestID}); err != nil {
		return
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "requests:updated")
	}
}

func (a *Application) shutdown(_ context.Context) {
	if a.ctx != nil && a.notificationsReady {
		runtime.CleanupNotifications(a.ctx)
	}
	if a.server != nil {
		_ = a.server.Close()
	}
	if a.owned != nil {
		_ = a.owned.Close()
	}
}

func (a *Application) Execute(command string, arguments map[string]string) (any, error) {
	if a.startupErr != nil {
		return nil, a.startupErr
	}
	if a.executor == nil {
		return nil, errors.New("desktop bridge is not ready")
	}
	value, err := a.executor.Execute(context.Background(), command, arguments)
	if err != nil {
		if command == "bridge.start" || command == "bridge.restart" {
			a.setBridgeStartupError(err)
		}
		return nil, err
	}
	if command == "bridge.start" || command == "bridge.restart" {
		a.setBridgeStartupError(nil)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if command == "status" {
		if bridgeErr := a.currentBridgeStartupError(); bridgeErr != nil {
			var snapshot model.Snapshot
			if err := json.Unmarshal(data, &snapshot); err != nil {
				return nil, err
			}
			snapshot.Bridge.Error = bridgeErr.Error()
			data, err = json.Marshal(snapshot)
			if err != nil {
				return nil, err
			}
		}
	}
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (a *Application) setBridgeStartupError(err error) {
	a.bridgeErrMu.Lock()
	a.bridgeStartupErr = err
	a.bridgeErrMu.Unlock()
}

func (a *Application) currentBridgeStartupError() error {
	a.bridgeErrMu.RLock()
	defer a.bridgeErrMu.RUnlock()
	return a.bridgeStartupErr
}

func (a *Application) OpenExternal(target string) error {
	if target != "docs" && target != "repository" {
		return errors.New("unsupported external target")
	}
	targetURL := "https://github.com/cryptoadvance/specter-virtual-host"
	if target == "docs" {
		targetURL += "#specter-virtual-host"
	}
	if a.ctx != nil {
		runtime.BrowserOpenURL(a.ctx, targetURL)
	}
	return nil
}
