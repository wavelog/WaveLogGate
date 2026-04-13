# WavelogGate

WavelogGate is a desktop gateway application that connects amateur radio logging software (WSJT-X, FLDigi) and radio control hardware (FLRig, Hamlib) to the [Wavelog](https://github.com/wavelog/wavelog) web logging platform.

Built with Go + [Wails v2](https://wails.io) + Svelte. Ships as a single self-contained binary — no runtime dependencies.

---

## User Manual

### Network ports

| Port | Protocol | Direction | Purpose |
|------|----------|-----------|---------|
| 2333 (configurable) | UDP | Inbound | QSO log packets from WSJT-X / FLDigi |
| 54321 | HTTP | Inbound | QSY requests from Wavelog (`GET /{freq}/{mode}`) |
| 54322 | WebSocket | Outbound | Live radio status broadcast |

If any of these ports is already in use, a message is shown in the Status tab. Stop the conflicting application and restart WavelogGate.

---

### Configuration tab

#### Wavelog

| Field | Description |
|-------|-------------|
| URL | Full Wavelog URL including `index.php`, e.g. `https://log.example.com/index.php` |
| API Key | Wavelog API key (found in Wavelog → Settings) |
| Station | Station profile dropdown — populated automatically from Wavelog after entering URL and key |
| Radio name | Name sent with radio status updates (default: `WLGate`) |

Press **↻** to reload the station list without leaving the field.

#### Radio Control

Select the backend that matches your setup:

| Type | Description |
|------|-------------|
| None | No radio control — WavelogGate only forwards UDP log entries |
| FLRig | Connects to a running FLRig instance via XML-RPC |
| Hamlib | Connects to a running `rigctld` daemon via TCP |

Enter the **Host** and **Port** for the chosen backend. Defaults are `127.0.0.1:12345` (FLRig) and `127.0.0.1:4532` (Hamlib).

**Set MODE on QSY** — when Wavelog sends a QSY request, also change the radio mode (LSB below 8 MHz, USB above).

**Ignore Power** (Hamlib only) — skip reading TX power, useful for rigs where Hamlib reports power unreliably.

#### Buttons

| Button | Action |
|--------|--------|
| 💾 Save | Save the current profile settings to disk |
| Profiles | Open the profile manager (create / rename / delete / switch) |
| Test | Send a demo QSO to Wavelog's dry-run endpoint to verify connectivity |
| ⚙ Advanced | Configure UDP port and enable/disable the UDP listener |
| Quit | Exit the application |

---

### Profiles

WavelogGate supports multiple named configuration profiles. A minimum of two profiles must exist at all times.

- **Switch** — activates the selected profile; radio poller and Wavelog client switch immediately.
- **Rename** — change the display name of any profile.
- **Add** — create a new profile with default (empty) settings.
- **Delete** — remove a profile (disabled when only two remain or for the active profile).

Unsaved field changes are lost when switching profiles — save first if needed.

---

### Status tab

- **TRX display** — shows the current frequency and mode polled from the radio (updates every second).
- **Status messages** — UDP listener startup confirmation, errors, etc.
- **QSO result** — shows a green alert on successful Wavelog submission, red on failure, with callsign / band / mode details.

---

### UDP Logger Setup (WSJT-X / FLDigi)

#### WSJT-X

1. Open **WSJT-X → File → Settings → Reporting**
2. Enable **Secondary UDP Server**
3. Set **Server name**: `localhost` (or the WavelogGate machine IP)
4. Set **Server port**: `2333`

> Use **Secondary UDP Server** only — the primary server sends binary protocol packets that WavelogGate does not handle.

#### FLDigi

1. Open **FLDigi → Configure → User Interface → Logging**
2. Enable **UDP** log output
3. Set host to `localhost` and port to `2333`

---

### Radio Control Setup

#### FLRig

1. Install and launch [FLRig](http://www.w1hkj.com/), configure it for your radio.
2. FLRig's XML-RPC server runs on port **12345** by default — no additional setup needed.
3. In WavelogGate, set Radio type to **FLRig**, host `127.0.0.1`, port `12345`, and save.

#### Hamlib (rigctld)

Start `rigctld` for your radio, for example:

```bash
# Icom IC-7300 on USB serial
rigctld -m 3073 -r /dev/ttyUSB0 -s 115200 -t 4532

# Kenwood TS-2000
rigctld -m 2 -r /dev/ttyUSB0 -s 4800 -t 4532
```

Find your radio's model number with `rigctl -l`. In WavelogGate, set Radio type to **Hamlib**, host `127.0.0.1`, port `4532`, and save.

---

### Rotator Control

WavelogGate can control an antenna rotator via a running `rotctld` (Hamlib) daemon.

#### Configuration

In the **Config → Rotator** section:

| Field | Description |
|-------|-------------|
| Host | IP address of the `rotctld` host (leave empty to disable rotator) |
| Port | TCP port of `rotctld` (default: `4533`) |
| Threshold Az | Minimum azimuth change in degrees before a move command is sent (default: `2°`) |
| Threshold El | Minimum elevation change in degrees before a move command is sent (default: `2°`) |
| Park Az / El | Target position for the **Park** command (degrees) |

Save the profile after changing these fields. The rotator panel in the Status tab only appears when a host is configured.

#### Status tab panel

```
ROTATOR  ● connected

Az: 123.4°   El:  45.0°

○ Off   ● HF  Az: 270°
        ○ SAT Az: 180°  El: 30°

[Park]
```

- **Follow Off** — rotator holds its position; no automatic moves.
- **Follow HF** — rotator tracks the bearing received from Wavelog's lookup result (`lookup_result` WebSocket message).
- **Follow SAT** — rotator tracks azimuth and elevation from Wavelog's satellite tracking (`satellite_position` WebSocket message).
- **Park** — switches follow to Off and moves to the configured park position, bypassing the movement threshold.

#### rotctld setup

Start `rotctld` for your rotator, for example:

```bash
# Yaesu G-5500 via serial
rotctld -m 603 -r /dev/ttyUSB0 -s 9600 -t 4533

# Dummy rotator for testing
rotctld -m 1 -r /dev/null -t 4533
```

Find your rotator's model number with `rotctl -l`. In WavelogGate, set Host `127.0.0.1`, Port `4533`, and save.

#### Follow mode and WebSocket integration

When Wavelog sends bearing data over the WebSocket connection (port 54322), WavelogGate forwards it to the rotator according to the active follow mode:

| WS message type | Follow mode | Action |
|-----------------|-------------|--------|
| `lookup_result` (contains `azimuth`) | HF | Move to the reported azimuth |
| `satellite_position` (contains `azimuth` + `elevation`) | SAT | Move to the reported az/el |

Bearing updates are rate-limited (150 ms minimum between moves). The bearing display in the Status tab updates immediately regardless of follow mode.

---

### Internal Radio (Internal Hamlib)

WavelogGate can launch and manage its own `rigctld` process — useful if you want a single application to handle everything without running a separate daemon.

#### Prerequisites

`rigctld` must be available on the system. WavelogGate searches in this order:

1. `~/.config/WavelogGate/hamlib/rigctld[.exe]` — a previously downloaded managed copy
2. Common platform-specific paths (e.g. Homebrew on macOS: `/opt/homebrew/bin/`, `/usr/local/bin/`)
3. System `PATH`

**Windows** — click **Download** in the Internal Hamlib settings to automatically fetch `rigctld.exe` and its DLLs from the latest Hamlib GitHub release.

**macOS** — install via Homebrew:
```bash
brew install hamlib
```

**Linux** — install via your package manager:
```bash
# Debian / Ubuntu
sudo apt install hamlib-utils

# Fedora / RHEL
sudo dnf install hamlib

# Arch / Manjaro
sudo pacman -S hamlib
```

After installing, click **Detect** in the Internal Hamlib settings so WavelogGate can locate the binary.

#### Configuration

Enable **Internal Hamlib** (select `InternalHamlib` as Radio type). The following fields become available:

| Field | Description |
|-------|-------------|
| Radio model | Hamlib model number — use the search box to find your radio (e.g. `IC-7300` → model 3073) |
| Serial port | Device path (e.g. `/dev/ttyUSB0`, `/dev/cu.usbserial-*`, `COM3`) |
| Baud rate | Serial baud rate matching your radio's CI-V / CAT setting |
| Parity | Serial parity: `none`, `odd`, or `even` (default: `none`) |
| Stop bits | Number of stop bits (0 = default; typically 1 or 2) |
| Handshake | Flow control: `none`, `rtscts`, or `xonxoff` (default: `none`) |
| rigctld port | TCP port that the managed `rigctld` will listen on (default: `4532`) |

WavelogGate automatically passes these settings to `rigctld` and monitors the process. Status is shown in the Status tab (`Stopped` / `Starting…` / `Running` / `Error: …`).

#### Notes

- The managed `rigctld` process is stopped and restarted whenever you switch profiles or change the Internal Hamlib settings.
- If `rigctld` exits unexpectedly the status changes to `Error` with a diagnostic message; fix the configuration and save to restart.
- When **Internal Hamlib** is active, WavelogGate also connects to the managed `rigctld` on the same port to poll frequency and mode — no separate Hamlib entry is needed.
- On macOS the serial port is often listed under `/dev/cu.usbserial-*`; use the serial port dropdown to enumerate detected ports.

---

### WebSocket broadcast

Any client can connect to `ws://localhost:54322` to receive live radio status:

```json
{
  "type": "radio_status",
  "frequency": 14225000,
  "mode": "USB",
  "power": 100,
  "radio": "WLGate",
  "timestamp": 1700000000000
}
```

A `{"type":"welcome","message":"..."}` message is sent on connect, followed immediately by the last known radio status.

---

### Troubleshooting

**Port conflict** — another application is using port 2333, 54321, or 54322. Find it with `lsof -i :<port>` (macOS/Linux) or `netstat -ano | findstr :<port>` (Windows) and stop it.

**Station dropdown empty** — check that the Wavelog URL (including `index.php`) and API key are correct, then press ↻.

**Test returns "wrong URL"** — the URL points to a page that returns HTML instead of JSON. Ensure the path ends with `index.php`.

**No QSOs appearing** — in WSJT-X, make sure you're using the **Secondary** UDP server, not the primary one.

**macOS quarantine (Apple Silicon)** — if the app is blocked after download, run:
```bash
xattr -d com.apple.quarantine /Applications/WavelogGate.app
```

---

## Building from Source

### Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| Go | 1.23+ | https://go.dev/dl/ |
| Wails CLI | v2.x | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` |
| Bun | any | https://bun.sh |

### Development (live reload)

```bash
cd WavelogGate-Go
wails dev
```

This starts a live-reload server — Go and Svelte changes are picked up automatically.

### Production build

```bash
wails build
```

The binary is placed in `build/bin/`. On macOS it produces a `.app` bundle, on Windows an `.exe`, on Linux a standalone binary.

### Build flags

| Flag | Effect |
|------|--------|
| `-clean` | Clean build cache before building |
| `-platform windows/amd64` | Cross-compile for Windows |
| `-nsis` | Generate Windows NSIS installer (requires NSIS) |
| `-upx` | Compress binary with UPX |

Example:
```bash
wails build -clean -platform darwin/arm64
```
