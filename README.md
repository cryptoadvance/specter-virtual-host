# Specter Virtual Host

Specter Virtual Host is one desktop application and one shared bridge core. It
connects a compatible browser simulator to wallet software through Specter
DIY's simulator USB protocol. The firmware and browser tooling live in
[`cryptoadvance/specter-diy`](https://github.com/cryptoadvance/specter-diy) and
[`cryptoadvance/specter-diy-web-simulator`](https://github.com/cryptoadvance/specter-diy-web-simulator).

<img width="1672" height="941" alt="Specter Virtual Host" src="https://github.com/user-attachments/assets/27e155d2-a5b8-4ca8-b79d-10d3bd209285" />

<img width="1535" height="810" alt="image" src="https://github.com/user-attachments/assets/6052b575-4ca9-44b5-b2c9-7ed591b1429b" />


Everything stays on the same computer:

- `127.0.0.1:8788` serves the connected simulator and browser bridge.
- `127.0.0.1:8789` exposes the simulator USB / HWI endpoint to wallet software.
- The bridge does not upload seed phrases, PSBTs, wallet data, or USB payloads.

Use public test data only. The simulator must finish wallet setup, reach
Applications, and have USB communication enabled before desktop discovery can
return its fingerprint. Any compatible website can connect in the default
open-origin mode; a website still needs to implement the Specter DIY bridge
protocol.

The GUI starts the bridge without opening a second simulator tab by default.
The browser simulator keeps its test-wallet state in that tab, so use the
already-configured simulator tab when one is open. The GUI will disable its
Open Simulator button while a simulator is connected to avoid replacing that
session.

## One app per platform

- Windows: `Specter-Virtual-Host.exe`
- macOS: `Specter Virtual Host.app`; its `Contents/MacOS/Specter-Virtual-Host`
  executable also accepts CLI commands.
- Linux: the `specter-virtual-host` package installs the
  `/usr/bin/specter-virtual-host` executable, desktop entry, and GUI runtime
  libraries. The package works on a server without a desktop session; no X11
  or Wayland session is needed for CLI or headless use.

Starting the app with no arguments opens the GUI. Supplying a command runs the
CLI in the same executable. The GUI, CLI, and headless server use the same core,
configuration file, bridge state, and trusted-sites policy. CLI commands use a
per-user local IPC endpoint when an app or server is already running.

## Origin policy and approval requests

The default policy is **open**: any valid HTTP or HTTPS origin can connect to
the local bridge. This makes the simulator work immediately after startup.

Enable **Advanced → Trusted websites only** to enforce the whitelist. The
Trusted Websites panel is shown only while that policy is enabled. Unknown sites
then trigger a native Specter Virtual Host notification with **Allow Once** and
**Allow Permanently** actions. The request is held for up to 30 seconds; dismissing
the notification does not approve it, and unanswered requests are denied on timeout.
Permanent approval adds the normalized origin to the shared trusted-sites list.
The same request can be handled in the GUI or with the headless CLI. If
notifications are disabled, unknown sites are denied.

## CLI

Run these commands from PowerShell, CMD, Windows Terminal, macOS Terminal, or a
Linux shell. The executable prints command results to stdout, errors to stderr,
and returns nonzero exit codes for invalid commands or unavailable services.
Use `--json` for machine-readable output.

```text
Specter-Virtual-Host status
Specter-Virtual-Host status --json
Specter-Virtual-Host bridge start
Specter-Virtual-Host bridge stop
Specter-Virtual-Host settings set origin-policy trusted
Specter-Virtual-Host sites list
Specter-Virtual-Host sites add https://example.com
Specter-Virtual-Host requests list
Specter-Virtual-Host requests allow-once REQUEST_ID
Specter-Virtual-Host requests trust REQUEST_ID
Specter-Virtual-Host requests deny REQUEST_ID
```

Redirects and pipes work normally, for example:

```powershell
Specter-Virtual-Host status > output.txt
Specter-Virtual-Host status | Select-String Bridge
```

```cmd
Specter-Virtual-Host.exe status > output.txt
Specter-Virtual-Host.exe status | findstr Bridge
```

With the bridge stopped, `status`, settings, trusted-site management, and log
commands still use the normal per-user config. Commands that control a running
bridge connect to its local IPC endpoint and never start a second bridge.
`bridge start` starts a background headless instance if none is running.

### macOS

Double-click `Specter Virtual Host.app` to open the GUI. In Terminal, use the
binary inside the app bundle for CLI calls and scripts:

```sh
"/Applications/Specter Virtual Host.app/Contents/MacOS/Specter-Virtual-Host" status
"/Applications/Specter Virtual Host.app/Contents/MacOS/Specter-Virtual-Host" settings set origin-policy trusted
```

### Linux

The `.deb` package provides a desktop entry and the CLI executable:

```sh
specter-virtual-host status
specter-virtual-host status | grep Bridge
specter-virtual-host status > output.txt
```

The app's GTK/WebKit runtime libraries are package dependencies. A display
server is not required for CLI or headless operation.

## Headless and services

`serve --headless` runs the bridge in the foreground without opening a GUI,
window, or tray icon. It handles Ctrl+C and normal termination signals and is
suitable for Docker, SSH, automation, and service managers. `--headless` by
itself is an equivalent form.

```text
Specter-Virtual-Host serve --headless
Specter-Virtual-Host --headless
Specter-Virtual-Host status
Specter-Virtual-Host bridge stop
Specter-Virtual-Host settings set origin-policy trusted
```

The headless process reads and writes the same settings as the GUI. When the
trusted policy is on, requests can be approved without a GUI:

```text
Specter-Virtual-Host requests list
Specter-Virtual-Host requests allow-once REQUEST_ID
Specter-Virtual-Host requests trust REQUEST_ID
Specter-Virtual-Host requests deny REQUEST_ID
```

Example service files are in [`contrib/`](contrib/):

- Linux: copy `contrib/systemd/specter-virtual-host.service` to
  `~/.config/systemd/user/`, then run `systemctl --user daemon-reload` and
  `systemctl --user enable --now specter-virtual-host.service`. For servers
  that must run without an interactive login, enable user lingering with
  `loginctl enable-linger "$USER"`.
- macOS: edit the app path in
  `contrib/launchd/com.cryptoadvance.specter-virtual-host.plist`, install it in
  `~/Library/LaunchAgents/`, then load it with `launchctl bootstrap gui/$(id -u)
  ~/Library/LaunchAgents/com.cryptoadvance.specter-virtual-host.plist`.
- Windows: run an elevated PowerShell and create a service with
  `sc.exe create SpecterVirtualHost binPath= '"C:\Program Files\Specter Virtual Host\Specter-Virtual-Host.exe" serve --headless' start= auto`.
  Run the service under the same Windows account used by the CLI so it shares
  that account's settings and protected IPC endpoint. Manage it with
  `sc.exe start` and `sc.exe stop`.

Build and run the Docker image from the repository root:

```sh
wails build -skipbindings -tags webkit2_41 -o specter-virtual-host
docker build -f packaging/docker/Dockerfile -t specter-virtual-host .
docker run --rm --name specter-virtual-host \
  -p 127.0.0.1:8788:8788 -p 127.0.0.1:8789:8789 \
  -v specter-virtual-host-data:/data specter-virtual-host
```

The container has no display server. It includes the GTK/WebKit runtime shared
libraries needed to load the one GUI-capable binary, and starts that binary in
headless mode. The container example binds its ports internally and publishes
them only on host loopback; change the published host address only if access
from other machines is intentional. Use `docker exec` with
`specter-virtual-host status` or other CLI commands to control it.

## Development

The GUI uses Wails v2.15.0. Run `wails doctor` to install platform build
dependencies, then:

```sh
go test ./...
wails dev -skipbindings
wails build -skipbindings
```

Linux builds use the WebKit2GTK 4.1 ABI (`-tags webkit2_41`). The project CI
builds Windows, macOS, Linux x64, and Linux arm64 packages on their native
platform runners.

## License

MIT. See [LICENSE](LICENSE).
