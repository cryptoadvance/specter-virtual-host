# Specter Virtual Host

Specter Virtual Host is a cross-platform local bridge that connects the
Specter DIY web simulator to desktop wallet software through Specter DIY's
official simulator USB protocol.

<img width="1672" height="941" alt="specter-virtual-host-grafik" src="https://github.com/user-attachments/assets/27e155d2-a5b8-4ca8-b79d-10d3bd209285" />

<img width="1535" height="810" alt="image" src="https://github.com/user-attachments/assets/6052b575-4ca9-44b5-b2c9-7ed591b1429b" />


Everything stays on the same computer:

- `127.0.0.1:8788` serves the connected simulator page and browser bridge.
- `127.0.0.1:8789` exposes Specter DIY's simulator USB endpoint.

The bridge does not upload seed phrases, PSBTs, wallet data or USB payloads to
a ClavaStack server. Use public test data only. The simulator must complete
initial wallet setup, reach Applications, and have USB communication enabled
before desktop discovery can return its fingerprint.

## Downloads

Tagged releases publish binaries for Windows x64, Linux x64, macOS Intel
(x64), and macOS Apple Silicon (arm64). Linux and macOS users may need to run
`chmod +x` on the downloaded binary. Unsigned macOS builds can require explicit
Gatekeeper approval.

## Usage

Run the matching release binary and keep its window open. By default it opens
the local connected simulator and proxies the configured simulator site. For
local website development:

```text
specter-virtual-host --site http://127.0.0.1:8765
```

Then open `http://127.0.0.1:8788/connected`, finish the public test-wallet
setup, enable **Device settings → Communication → USB communication**, confirm
the reboot, and rescan hardware devices in Specter Desktop.

## Development

```text
go test ./...
go run . --site http://127.0.0.1:8765 --no-open
```

The WebSocket bridge accepts only local browser origins or the production
ClavaStack origin and binds only to loopback addresses.

## License

MIT. See [LICENSE](LICENSE).
