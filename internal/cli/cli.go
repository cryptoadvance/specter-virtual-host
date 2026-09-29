package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cryptoadvance/specter-virtual-host/internal/control"
	"github.com/cryptoadvance/specter-virtual-host/internal/core"
)

const (
	ExitOK       = 0
	ExitUsage    = 2
	ExitNotReady = 3
	ExitFailure  = 1
)

type Runner struct {
	Version          string
	ConfigPath       string
	Stdout           io.Writer
	Stderr           io.Writer
	ControlAvailable func(context.Context) bool
	ControlExecutor  func() core.Executor
}

func (r Runner) Run(ctx context.Context, arguments []string) int {
	if r.Stdout == nil {
		r.Stdout = os.Stdout
	}
	if r.Stderr == nil {
		r.Stderr = os.Stderr
	}
	if len(arguments) == 0 {
		r.usage(r.Stderr)
		return ExitUsage
	}
	command, commandArguments, jsonOutput, err := parse(arguments)
	if err != nil {
		fmt.Fprintln(r.Stderr, "Error:", err)
		r.usage(r.Stderr)
		return ExitUsage
	}
	if command == "help" || command == "version" {
		if err := r.print(command, nil, jsonOutput); err != nil {
			fmt.Fprintln(r.Stderr, "Error:", err)
			return ExitFailure
		}
		return ExitOK
	}

	executor, local, err := r.executor(ctx, command)
	if err != nil {
		fmt.Fprintln(r.Stderr, "Error:", err)
		return ExitFailure
	}
	if local != nil {
		defer local.Close()
	}

	if command == "bridge.start" && local != nil {
		if err := startBackground(); err != nil {
			fmt.Fprintln(r.Stderr, "Error: could not start headless bridge:", err)
			return ExitFailure
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if r.controlAvailable(ctx) {
				executor = r.controlExecutor()
				local = nil
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if local != nil {
			fmt.Fprintln(r.Stderr, "Error: headless bridge did not become ready")
			return ExitNotReady
		}
	}

	result, err := executor.Execute(ctx, command, commandArguments)
	if err != nil {
		fmt.Fprintln(r.Stderr, "Error:", err)
		if control.IsUnavailable(err) {
			return ExitNotReady
		}
		return ExitFailure
	}
	if err := r.print(command, result, jsonOutput); err != nil {
		fmt.Fprintln(r.Stderr, "Error:", err)
		return ExitFailure
	}
	return ExitOK
}

func (r Runner) executor(ctx context.Context, command string) (core.Executor, *core.Core, error) {
	if r.controlAvailable(ctx) {
		return r.controlExecutor(), nil, nil
	}
	local, err := core.New(r.Version, "offline", r.ConfigPath)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case command == "status", command == "settings.get", command == "settings.set",
		strings.HasPrefix(command, "sites."), command == "log.show", command == "log.clear",
		command == "bridge.start", command == "bridge.stop", command == "requests.list":
		return local, local, nil
	default:
		_ = local.Close()
		return nil, nil, fmt.Errorf("no running instance; start the app or use serve --headless")
	}
}

func (r Runner) controlAvailable(ctx context.Context) bool {
	if r.ControlAvailable != nil {
		return r.ControlAvailable(ctx)
	}
	return control.Available(ctx)
}

func (r Runner) controlExecutor() core.Executor {
	if r.ControlExecutor != nil {
		return r.ControlExecutor()
	}
	return control.NewClient()
}

func parse(arguments []string) (string, map[string]string, bool, error) {
	jsonOutput := false
	filtered := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		if argument == "--json" {
			jsonOutput = true
			continue
		}
		filtered = append(filtered, argument)
	}
	if len(filtered) == 0 {
		return "", nil, false, errors.New("a command is required")
	}
	args := map[string]string{}
	switch filtered[0] {
	case "status":
		return "status", args, jsonOutput, requireCount(filtered, 1)
	case "bridge":
		if len(filtered) != 2 || (filtered[1] != "start" && filtered[1] != "stop" && filtered[1] != "restart") {
			return "", nil, false, errors.New("usage: bridge start|stop|restart")
		}
		return "bridge." + filtered[1], args, jsonOutput, nil
	case "wallet":
		if len(filtered) != 2 || (filtered[1] != "disconnect" && filtered[1] != "allow") {
			return "", nil, false, errors.New("usage: wallet disconnect|allow")
		}
		return "wallet." + filtered[1], args, jsonOutput, nil
	case "simulator":
		if len(filtered) != 2 || filtered[1] != "open" {
			return "", nil, false, errors.New("usage: simulator open")
		}
		return "simulator.open", args, jsonOutput, nil
	case "settings":
		if len(filtered) == 2 && filtered[1] == "get" {
			return "settings.get", args, jsonOutput, nil
		}
		if len(filtered) == 4 && filtered[1] == "set" {
			args["key"], args["value"] = filtered[2], filtered[3]
			return "settings.set", args, jsonOutput, nil
		}
		return "", nil, false, errors.New("usage: settings get | settings set KEY VALUE")
	case "sites":
		if len(filtered) == 2 && filtered[1] == "list" {
			return "sites.list", args, jsonOutput, nil
		}
		if len(filtered) == 3 && contains([]string{"add", "remove", "enable", "disable"}, filtered[1]) {
			args["origin"] = filtered[2]
			return "sites." + filtered[1], args, jsonOutput, nil
		}
		return "", nil, false, errors.New("usage: sites list | sites add|remove|enable|disable ORIGIN")
	case "requests":
		if len(filtered) == 2 && filtered[1] == "list" {
			return "requests.list", args, jsonOutput, nil
		}
		if len(filtered) == 3 && contains([]string{"allow-once", "trust", "deny"}, filtered[1]) {
			args["id"] = filtered[2]
			return "requests." + filtered[1], args, jsonOutput, nil
		}
		return "", nil, false, errors.New("usage: requests list | requests allow-once|trust|deny REQUEST_ID")
	case "log":
		if len(filtered) != 2 || (filtered[1] != "show" && filtered[1] != "clear") {
			return "", nil, false, errors.New("usage: log show|clear")
		}
		return "log." + filtered[1], args, jsonOutput, nil
	case "version", "--version", "-version":
		return "version", args, jsonOutput, requireCount(filtered, 1)
	case "help", "--help", "-h":
		return "help", args, jsonOutput, nil
	default:
		return "", nil, false, fmt.Errorf("unknown command %q", filtered[0])
	}
}

func requireCount(arguments []string, count int) error {
	if len(arguments) != count {
		return errors.New("unexpected arguments")
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func (r Runner) print(command string, value any, jsonOutput bool) error {
	if command == "help" {
		r.usage(r.Stdout)
		return nil
	}
	if command == "version" {
		fmt.Fprintln(r.Stdout, r.Version)
		return nil
	}
	if jsonOutput || command != "status" {
		encoder := json.NewEncoder(r.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var status struct {
		Version string `json:"version"`
		Mode    string `json:"mode"`
		Bridge  struct {
			Running                 bool   `json:"running"`
			BrowserConnected        bool   `json:"browserConnected"`
			BrowserRecoveryRequired bool   `json:"browserRecoveryRequired"`
			BrowserOrigin           string `json:"browserOrigin"`
			WalletConnected         bool   `json:"walletConnected"`
			WalletAllowed           bool   `json:"walletAllowed"`
			WebAddress              string `json:"webAddress"`
			HWIAddress              string `json:"hwiAddress"`
		} `json:"bridge"`
		Settings struct {
			OriginPolicy string `json:"originPolicy"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return err
	}
	fmt.Fprintf(r.Stdout, "Specter Virtual Host %s\n", status.Version)
	fmt.Fprintf(r.Stdout, "Mode:              %s\n", status.Mode)
	fmt.Fprintf(r.Stdout, "Bridge:            %s\n", onOff(status.Bridge.Running))
	fmt.Fprintf(r.Stdout, "Browser:           %s", onOff(status.Bridge.BrowserConnected))
	if status.Bridge.BrowserOrigin != "" {
		fmt.Fprintf(r.Stdout, " (%s)", status.Bridge.BrowserOrigin)
	}
	if status.Bridge.BrowserRecoveryRequired {
		fmt.Fprint(r.Stdout, " (reload simulator tab to recover)")
	}
	fmt.Fprintln(r.Stdout)
	walletRequest := "idle"
	if status.Bridge.BrowserRecoveryRequired {
		walletRequest = "recovery required"
	} else if status.Bridge.WalletConnected {
		walletRequest = "active"
	}
	fmt.Fprintf(r.Stdout, "Wallet request:    %s\n", walletRequest)
	fmt.Fprintf(r.Stdout, "Wallet connections:%s\n", map[bool]string{true: " allowed", false: " blocked"}[status.Bridge.WalletAllowed])
	fmt.Fprintf(r.Stdout, "Origin policy:     %s\n", status.Settings.OriginPolicy)
	fmt.Fprintf(r.Stdout, "Browser endpoint:  %s\n", status.Bridge.WebAddress)
	fmt.Fprintf(r.Stdout, "HWI endpoint:      %s\n", status.Bridge.HWIAddress)
	return nil
}

func onOff(value bool) string {
	if value {
		return "running"
	}
	return "stopped"
}

func (r Runner) usage(writer io.Writer) {
	commands := []string{
		"status [--json]", "bridge start|stop|restart", "wallet disconnect|allow",
		"simulator open", "settings get [--json]", "settings set KEY VALUE",
		"sites list [--json]", "sites add|remove|enable|disable ORIGIN",
		"requests list|allow-once|trust|deny [REQUEST_ID]", "log show|clear [--json]",
		"serve --headless", "version",
	}
	sort.Strings(commands)
	fmt.Fprintln(writer, "Usage: Specter-Virtual-Host COMMAND")
	for _, command := range commands {
		fmt.Fprintln(writer, "  "+command)
	}
}
