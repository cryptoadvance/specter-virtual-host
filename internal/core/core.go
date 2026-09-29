package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/cryptoadvance/specter-virtual-host/internal/activity"
	"github.com/cryptoadvance/specter-virtual-host/internal/bridge"
	"github.com/cryptoadvance/specter-virtual-host/internal/config"
	"github.com/cryptoadvance/specter-virtual-host/internal/model"
	"github.com/cryptoadvance/specter-virtual-host/internal/policy"
)

type Executor interface {
	Execute(context.Context, string, map[string]string) (any, error)
}

type Core struct {
	mu       sync.RWMutex
	version  string
	mode     string
	config   *config.Store
	activity *activity.Store
	bridge   *bridge.Service
}

func New(version, mode, configPath string) (*Core, error) {
	webAddress := os.Getenv("SPECTER_VIRTUAL_HOST_WEB_ADDRESS")
	if webAddress == "" {
		webAddress = model.WebAddress
	}
	hwiAddress := os.Getenv("SPECTER_VIRTUAL_HOST_HWI_ADDRESS")
	if hwiAddress == "" {
		hwiAddress = model.HWIAddress
	}
	return NewWithAddresses(version, mode, configPath, webAddress, hwiAddress)
}

func NewWithAddresses(version, mode, configPath, webAddress, hwiAddress string) (*Core, error) {
	settings, err := config.Open(configPath)
	if err != nil {
		return nil, err
	}
	activityStore, err := activity.Open(settings.Path())
	if err != nil {
		return nil, err
	}
	instance := &Core{version: version, mode: mode, config: settings, activity: activityStore}
	instance.bridge = bridge.NewWithAddresses(version, webAddress, hwiAddress, settings.Get, func(kind, origin, message, result string) {
		activityStore.Add(kind, origin, message, result)
	})
	return instance, nil
}

func (c *Core) SetMode(mode string) {
	c.mu.Lock()
	c.mode = mode
	c.mu.Unlock()
}

func (c *Core) StartBridge() error { return c.bridge.Start() }
func (c *Core) StopBridge() error  { return c.bridge.Stop() }

func (c *Core) Close() error { return c.bridge.Stop() }

func (c *Core) Snapshot() model.Snapshot {
	c.mu.RLock()
	mode := c.mode
	c.mu.RUnlock()
	return model.Snapshot{
		Version: c.version, Mode: mode, Bridge: c.bridge.Status(),
		Settings: c.config.Get(), Activity: c.activity.List(), Requests: c.bridge.PendingRequests(),
	}
}

func (c *Core) Execute(_ context.Context, command string, arguments map[string]string) (any, error) {
	switch command {
	case "status":
		return c.Snapshot(), nil
	case "bridge.start":
		err := c.bridge.Start()
		return c.Snapshot(), err
	case "bridge.stop":
		err := c.bridge.Stop()
		return c.Snapshot(), err
	case "bridge.restart":
		if err := c.bridge.Stop(); err != nil {
			return nil, err
		}
		err := c.bridge.Start()
		return c.Snapshot(), err
	case "wallet.disconnect":
		c.bridge.DisconnectWallet()
		return c.Snapshot(), nil
	case "wallet.allow":
		c.bridge.AllowWallet()
		return c.Snapshot(), nil
	case "simulator.open":
		return c.Snapshot(), c.bridge.OpenSimulator()
	case "settings.get":
		return c.config.Get(), nil
	case "settings.set":
		return c.setSetting(arguments["key"], arguments["value"])
	case "sites.list":
		return c.config.Get().TrustedSites, nil
	case "sites.add":
		return c.addSite(arguments["origin"])
	case "sites.remove":
		return c.removeSite(arguments["origin"])
	case "sites.enable":
		return c.setSiteEnabled(arguments["origin"], true)
	case "sites.disable":
		return c.setSiteEnabled(arguments["origin"], false)
	case "log.show":
		return c.activity.List(), nil
	case "log.clear":
		err := c.activity.Clear()
		return c.Snapshot(), err
	case "requests.list":
		return c.bridge.PendingRequests(), nil
	case "requests.allow-once":
		return c.resolveRequest(arguments["id"], true, nil)
	case "requests.trust":
		return c.resolveRequest(arguments["id"], true, func(origin string) error {
			_, err := c.addSite(origin)
			return err
		})
	case "requests.deny":
		return c.resolveRequest(arguments["id"], false, nil)
	default:
		return nil, fmt.Errorf("unknown command %q", command)
	}
}

func (c *Core) resolveRequest(id string, allowed bool, beforeAllow func(string) error) (model.Snapshot, error) {
	if id == "" {
		return model.Snapshot{}, errors.New("request id is required")
	}
	if _, found, err := c.bridge.ResolveRequest(id, allowed, beforeAllow); err != nil {
		return model.Snapshot{}, err
	} else if !found {
		return model.Snapshot{}, errors.New("connection request expired or was already handled")
	}
	return c.Snapshot(), nil
}

func (c *Core) setSetting(key, value string) (model.Settings, error) {
	if key == "" {
		return model.Settings{}, errors.New("setting key is required")
	}
	settings, err := c.config.Update(func(settings *model.Settings) error {
		switch key {
		case "origin-policy":
			policyValue := model.OriginPolicy(strings.ToLower(value))
			if policyValue != model.OriginPolicyOpen && policyValue != model.OriginPolicyTrusted {
				return errors.New("origin-policy must be open or trusted")
			}
			settings.OriginPolicy = policyValue
		case "notify-new-site":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return errors.New("notify-new-site must be true or false")
			}
			settings.NotifyNewSite = parsed
		case "start-bridge-on-launch":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return errors.New("start-bridge-on-launch must be true or false")
			}
			settings.StartBridgeOnLaunch = parsed
		case "open-simulator-on-launch":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return errors.New("open-simulator-on-launch must be true or false")
			}
			settings.OpenSimulatorOnLaunch = parsed
		case "site":
			parsed, err := url.Parse(value)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return errors.New("site must be a valid http or https URL")
			}
			settings.Site = parsed.String()
		default:
			return fmt.Errorf("unknown setting %q", key)
		}
		return nil
	})
	if err != nil {
		return model.Settings{}, err
	}
	if key == "origin-policy" && settings.OriginPolicy == model.OriginPolicyOpen {
		c.bridge.ResolvePending(true)
	} else if key == "notify-new-site" && !settings.NotifyNewSite {
		c.bridge.ResolvePending(false)
	}
	return settings, nil
}

func (c *Core) addSite(rawOrigin string) ([]model.TrustedSite, error) {
	origin, err := policy.NormalizeOrigin(rawOrigin)
	if err != nil {
		return nil, err
	}
	settings, err := c.config.Update(func(settings *model.Settings) error {
		for index := range settings.TrustedSites {
			if settings.TrustedSites[index].Origin == origin {
				settings.TrustedSites[index].Enabled = true
				return nil
			}
		}
		settings.TrustedSites = append(settings.TrustedSites, model.TrustedSite{Origin: origin, Enabled: true})
		return nil
	})
	return settings.TrustedSites, err
}

func (c *Core) removeSite(rawOrigin string) ([]model.TrustedSite, error) {
	origin, err := policy.NormalizeOrigin(rawOrigin)
	if err != nil {
		return nil, err
	}
	settings, err := c.config.Update(func(settings *model.Settings) error {
		for index, site := range settings.TrustedSites {
			if site.Origin != origin {
				continue
			}
			if site.BuiltIn {
				return errors.New("built-in sites can be disabled but not removed")
			}
			settings.TrustedSites = append(settings.TrustedSites[:index], settings.TrustedSites[index+1:]...)
			return nil
		}
		return fmt.Errorf("trusted site %s was not found", origin)
	})
	return settings.TrustedSites, err
}

func (c *Core) setSiteEnabled(rawOrigin string, enabled bool) ([]model.TrustedSite, error) {
	origin, err := policy.NormalizeOrigin(rawOrigin)
	if err != nil {
		return nil, err
	}
	settings, err := c.config.Update(func(settings *model.Settings) error {
		for index := range settings.TrustedSites {
			if settings.TrustedSites[index].Origin == origin {
				settings.TrustedSites[index].Enabled = enabled
				return nil
			}
		}
		return fmt.Errorf("trusted site %s was not found", origin)
	})
	return settings.TrustedSites, err
}
