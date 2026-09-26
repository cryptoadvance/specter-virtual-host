# Specter Virtual Host

Specter Virtual Host is a cross-platform local bridge that connects the
[`cryptoadvance/specter-diy` browser simulator](https://cryptoadvance.github.io/specter-diy/)
to Specter Desktop through Specter DIY's simulator USB protocol. The firmware
and browser tooling live in
[`cryptoadvance/specter-diy`](https://github.com/cryptoadvance/specter-diy) and
[`cryptoadvance/specter-diy-web-simulator`](https://github.com/cryptoadvance/specter-diy-web-simulator).

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

Download the matching binary from the
[latest Virtual Host release](https://github.com/cryptoadvance/specter-virtual-host/releases/latest)
and keep its window open. By default it opens the local connected simulator
and proxies the official GitHub Pages site. Until a new release is published,
older binaries may still use the legacy site; override it explicitly with
`--site https://cryptoadvance.github.io/specter-diy/`. For local website development:

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

The WebSocket bridge accepts only local browser origins, the official
`cryptoadvance.github.io` Pages origin, or the legacy ClavaStack origin. It
binds only to loopback addresses; firmware USB bytes are forwarded between the
browser worker and Specter Desktop on this computer.

## License

MIT. See [LICENSE](LICENSE).
