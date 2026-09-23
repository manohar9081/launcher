# Launcher

Launcher is a local dashboard for running and observing the small web tools in
this repository. It provides one place to start, stop, inspect, and open the
apps that make up the project.

The repository currently contains:

- **Project Launcher** (`Launcher/`) - a Go HTTP dashboard and JSON API for
  managing local apps. It tracks processes, detects apps that are already
  listening, tails app logs, and can expose network URLs for apps marked as
  network-enabled.
- **AppScope Monitor** (`Monitor/`) - a cross-platform Go privacy and network
  activity monitor. Its local dashboard collects camera, microphone, file,
  device, login, battery, application-focus, and network events when the
  required operating-system facilities are available.
- **PyMLOps** (`MLOPS/`) - a self-contained HTML learning page for Python and
  MLOps.
- **DevOps Cheatsheets** (`Cheatsheet/`) - a self-contained HTML reference
  page.

The launcher is intentionally local-first. It does not provide authentication
or user management, so do not expose its bind address to an untrusted network.

## How it works

1. `Launcher/main.go` loads `Launcher/apps.json`.
2. Each configured app supplies a directory, port, and either a shell command
   or executable plus arguments.
3. The launcher serves its dashboard on port `9090` by default.
4. Starting an app launches it in its configured directory, substitutes
   `{port}` in commands and arguments, and records its PID in `Launcher/state.json`.
5. The dashboard probes each configured port, so it can also show services that
   were started outside the launcher.
6. At startup, the launcher scans sibling folders for runnable Go HTTP modules
   and HTML projects that are not already listed in `apps.json`.

The launcher API is:

| Method | Endpoint | Purpose |
| --- | --- | --- |
| `GET` | `/` | Open the launcher dashboard |
| `GET` | `/api/apps` | Read app status |
| `POST` | `/api/start` | Start one app, with JSON body `{"id":"monitor"}` |
| `POST` | `/api/stop` | Stop one app |
| `POST` | `/api/all/start` | Start every stopped app |
| `POST` | `/api/all/stop` | Stop every running app |
| `GET` | `/api/logs?id=monitor` | Read the latest app log lines |
| `GET` | `/api/health` | Read launcher health and LAN information |

## Prerequisites

- Go **1.27 or newer**.
- Python 3 if you want to run the two static sites using the commands already
  present in `Launcher/apps.json`.
- A supported operating system: Windows, Linux, or macOS. Some monitor
  collectors are platform-specific and may be unavailable without the
  corresponding OS command, service, permission, or Android `adb` setup.
- Network access on the first monitor build so Go can download
  `modernc.org/sqlite`.

## DIY: run the project

### 1. Start the launcher

From the repository root:

```powershell
cd Launcher
go run .
```

Open <http://127.0.0.1:9090>. To build a reusable Windows binary instead:

```powershell
go build -o launcher.exe .
.\launcher.exe
```

Useful launcher options:

```powershell
go run . --start-apps       # start configured apps after the dashboard starts
go run . --no-open          # do not open a browser automatically
go run . --kill-apps-on-exit
```

On Linux or macOS, the equivalent commands are:

```sh
cd Launcher
go run .
go run . --start-apps --no-open
```

The launcher listens on `0.0.0.0:9090` by default. Override this with
`HOST` and `PORT`:

```powershell
$env:HOST = "127.0.0.1"
$env:PORT = "8080"
go run .
```

```sh
HOST=127.0.0.1 PORT=8080 go run .
```

### 2. Start an individual static site without the launcher

The static projects need only a simple HTTP server:

```powershell
cd Cheatsheet
python -m http.server 8000 --bind 0.0.0.0
```

```powershell
cd MLOPS
python -m http.server 8137 --bind 0.0.0.0
```

Then visit `http://127.0.0.1:8000` or `http://127.0.0.1:8137`. The launcher
uses these same commands for the `cheatsheet` and `mlops` entries.

### 3. Build and run AppScope Monitor

```powershell
cd Monitor
go build ./...
go build -o monitor.exe ./cmd/monitor
.\monitor.exe --port 8765 --open
```

On Linux or macOS:

```sh
cd Monitor
go build ./...
go build -o monitor ./cmd/monitor
./monitor --port 8765 --open
```

The monitor defaults to `127.0.0.1:8765`. Use `--host`, `--port`, `--config`,
and `--debug` to customize it:

```powershell
.\monitor.exe --config .\config.json --host 127.0.0.1 --port 8765 --debug
```

If `config.json` does not exist, built-in defaults are used. A configuration
file can define `watched_dirs`, collector toggles, polling intervals,
retention, `data_dir`, and whether to open the browser. Monitor data is stored
in `data/monitor.db` by default.

## DIY: add another app

Add an entry to `Launcher/apps.json`:

```json
{
  "id": "my-app",
  "name": "My App",
  "icon": "🔧",
  "desc": "A local app",
  "category": "Tools",
  "dir": "../MyApp",
  "port": 9001,
  "cmd": "python app.py --port {port}",
  "urlPath": "/",
  "network": false
}
```

Important fields:

- `id` must be unique.
- `dir` is resolved relative to `Launcher/`.
- `port` is the port the launcher probes and displays.
- `cmd` is run in the app directory. `{port}` is replaced before execution.
- Use `binary` and `args` instead of `cmd` for a compiled executable.
- Set `network` to `true` only when showing a LAN URL is intentional.

Restart the launcher after changing `apps.json`. Apps with a sibling `go.mod`
or HTML entry page can also be discovered automatically; use `apps.json` when
you need stable metadata, a fixed port, or a custom start command.

## Repository layout

```text
.
├── Launcher/
│   ├── apps.json       # configured app registry
│   ├── index.html      # launcher dashboard
│   └── main.go        # launcher server and process manager
├── Monitor/
│   ├── cmd/monitor/    # monitor CLI
│   ├── collectors/     # platform-specific collectors
│   └── web/            # monitor dashboard assets
├── MLOPS/              # Python and MLOps learning page
└── Cheatsheet/         # DevOps reference page
```

## Troubleshooting

- **Port already in use:** choose another port in `apps.json`, pass
  `--port` to monitor, or stop the process currently listening on the port.
- **An app is shown as external:** it is listening on the configured port but
  was not started by this launcher instance. The launcher can still probe and
  stop it where the platform permits.
- **Monitor collectors are unavailable:** this is expected when a collector's
  OS command, permission, service, or device is missing. Check the startup
  status table and run with `--debug`.
- **The launcher cannot find an app directory:** paths in `apps.json` are
  relative to `Launcher/`, not the shell's current directory.
- **Browser does not open:** use the printed URL directly and ensure that
  `--no-open` or `NO_OPEN=1` is not set.

For component-specific API details and implementation notes, see
[`Launcher/README.md`](Launcher/README.md) and
[`Monitor/README.md`](Monitor/README.md).
