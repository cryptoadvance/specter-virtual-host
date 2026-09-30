package model

import "time"

const (
	WebAddress = "127.0.0.1:8788"
	HWIAddress = "127.0.0.1:8789"
)

type OriginPolicy string

const (
	OriginPolicyOpen    OriginPolicy = "open"
	OriginPolicyTrusted OriginPolicy = "trusted"
)

type TrustedSite struct {
	Origin  string `json:"origin"`
	Enabled bool   `json:"enabled"`
	BuiltIn bool   `json:"builtIn,omitempty"`
}

type Settings struct {
	Version               int           `json:"version"`
	Site                  string        `json:"site"`
	StartBridgeOnLaunch   bool          `json:"startBridgeOnLaunch"`
	OpenSimulatorOnLaunch bool          `json:"openSimulatorOnLaunch"`
	OriginPolicy          OriginPolicy  `json:"originPolicy"`
	NotifyNewSite         bool          `json:"notifyNewSite"`
	TrustedSites          []TrustedSite `json:"trustedSites"`
}

func DefaultSettings() Settings {
	return Settings{
		Version:               1,
		Site:                  "https://try.clavastack.com/",
		StartBridgeOnLaunch:   true,
		OpenSimulatorOnLaunch: false,
		OriginPolicy:          OriginPolicyOpen,
		NotifyNewSite:         true,
		TrustedSites: []TrustedSite{
			{Origin: "https://try.clavastack.com", Enabled: true, BuiltIn: true},
			{Origin: "https://cryptoadvance.github.io", Enabled: true, BuiltIn: true},
		},
	}
}

type Activity struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Origin  string    `json:"origin,omitempty"`
	Message string    `json:"message"`
	Result  string    `json:"result,omitempty"`
}

type PendingRequest struct {
	ID          string    `json:"id"`
	Origin      string    `json:"origin"`
	RequestedAt time.Time `json:"requestedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type BridgeStatus struct {
	Running                 bool   `json:"running"`
	Error                   string `json:"error,omitempty"`
	BrowserConnected        bool   `json:"browserConnected"`
	BrowserRecoveryRequired bool   `json:"browserRecoveryRequired"`
	BrowserOrigin           string `json:"browserOrigin,omitempty"`
	WalletConnected         bool   `json:"walletConnected"`
	WalletAllowed           bool   `json:"walletAllowed"`
	WebAddress              string `json:"webAddress"`
	HWIAddress              string `json:"hwiAddress"`
}

type Snapshot struct {
	Version  string           `json:"version"`
	Mode     string           `json:"mode"`
	Bridge   BridgeStatus     `json:"bridge"`
	Settings Settings         `json:"settings"`
	Activity []Activity       `json:"activity"`
	Requests []PendingRequest `json:"requests"`
}
