# tcpconnbenchmark

<img src="assets/tcpconn-logo.svg" alt="tcpconnbenchmark Logo" width="160">

[中文](README.md)

A TCP concurrent connection testing tool written in Go with [MyGo Native UI](https://github.com/egoist/mygo). It offers a graphical interface and a CLI. It only performs DNS resolution and TCP connections; it does not send HTTP requests or perform TLS handshakes.

## Features

- Test TCP ports 80, 443, or both, with live connection statistics and logs.
- Select an outbound interface or IPv4 / IPv6 source address; use custom domains, URLs, or IP addresses.
- Configure duration, connection rate, and failure threshold. Successful connections remain open until the test ends.
- Export complete connection records and a summary on demand.

Defaults: 360 seconds, 125 connection attempts per second, and a stop threshold of 50 failures. The rate can be set from 1 to 1,000 attempts per second.

## Build and run

Go 1.27.3 or later is required; disable CGO when building. Platform requirements are based on the [MyGo documentation](https://github.com/egoist/mygo/blob/main/docs/README.md#requirements); not every OS and architecture has been tested with this project.

### macOS

Requires macOS 12 or later; the UI uses Metal.

```bash
CGO_ENABLED=0 go build -trimpath -o tcpconnbenchmark .
./tcpconnbenchmark
```

Alternatively, run `make app` to create `tcpconnbenchmark.app`. Packaging attempts ad-hoc signing; public distribution requires your own signing and notarization.

### Linux

Supports x64 and arm64. The graphical interface requires GTK 3 and OpenGL, but not WebKitGTK.

```bash
CGO_ENABLED=0 go build -trimpath -o tcpconnbenchmark .
./tcpconnbenchmark
```

Binding an interface may require `CAP_NET_RAW`. When only an interface is selected, a permission failure can fall back to source-address binding; when a source address is explicitly selected, a binding failure is reported instead.

### Windows

Supports Windows 10/11 on x64 and arm64. The UI uses Direct3D 11 and does not require WebView2. Build and run in PowerShell:

```powershell
$env:CGO_ENABLED = "0"
go build -trimpath -o tcpconnbenchmark.exe .
.\tcpconnbenchmark.exe
```

### Command line

```bash
./tcpconnbenchmark -cli -duration 5 -mode both -rate 10 -domains "127.0.0.1"
./tcpconnbenchmark -list-interfaces
```

On Windows, replace `./tcpconnbenchmark` with `.\tcpconnbenchmark.exe`. The example connects to local ports 80 and 443; connections will be refused if no services are listening. Add `-save` to export results, or run with `-h` to see all options.

## Data and precautions

- Custom domains are stored in `domains.json` next to the executable; for a macOS `.app`, they are stored next to the bundle.
- Results are not exported by default. Use “Save results” in the GUI or `-save` in the CLI to write `results.csv` and `summary.txt` under `logs/` in the portable directory. Unsaved results are lost when starting a new test or exiting.
- Success rate is successes divided by successes plus failures; connections aborted on stop are excluded.
- High-rate or long tests can exhaust file handles or trigger target firewalls and rate limits. Test only within your authorization and the capacity of your machine and targets.

## Development

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
```
