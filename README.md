# TunnelDesktop

TunnelDesktop is an RDP rendezvous tunnel designed for a work PC that can only make outbound connections through a corporate HTTPS proxy. The normal relay is an Android phone at home with public IPv6 reachability. The work PC dials out to the phone, the home PC connects to the phone, and the phone pairs both outbound sessions so RDP reaches `127.0.0.1:3389` on the work PC.

Raw RDP is never exposed on the public internet listener. Public traffic uses TLS 1.3, mutual TLS, and a shared bearer token before any stream is opened.

## Table Of Contents

- [How It Works](#how-it-works)
- [Installation](#installation)
  - [1. Install Android Relay](#1-install-android-relay)
  - [2. Keep Android Alive](#2-keep-android-alive)
  - [3. Install Work Agent](#3-install-work-agent)
  - [4. Run Home Client](#4-run-home-client)
- [Deliverables](#deliverables)
  - [Android Relay App](#android-relay-app)
  - [Work Agent](#work-agent)
  - [Agent Installer And Configurator](#agent-installer-and-configurator)
  - [Home Client](#home-client)
  - [Dev Harness And VPS Fallback](#dev-harness-and-vps-fallback)
- [Security Model](#security-model)
- [Phase 0 Feasibility](#phase-0-feasibility)
- [Build Prerequisites](#build-prerequisites)
- [Build Commands](#build-commands)
- [PC Harness Workflow](#pc-harness-workflow)
- [Configuration And Bundle Format](#configuration-and-bundle-format)
- [Troubleshooting](#troubleshooting)
  - [Agent Service Does Not Start](#agent-service-does-not-start)
  - [Client Connects But RDP Fails](#client-connects-but-rdp-fails)
  - [Android Relay Stops](#android-relay-stops)
  - [Gradle Cannot Download Dependencies](#gradle-cannot-download-dependencies)
- [Development](#development)
- [Repository Layout](#repository-layout)
- [Status](#status)
- [Current Limitations](#current-limitations)

## How It Works

```text
Home PC
  mstsc -> 127.0.0.1:<client port>
      or -> PHONE_HOTSPOT_IP:3389
        |
        v
Android phone relay
  TLS :8443 public listener
  optional hotspot-only raw RDP listener
        |
        v
Work PC agent
  outbound HTTPS proxy CONNECT -> TLS -> phone
  yamux server
  per-stream dial -> 127.0.0.1:3389
```

The relay is mandatory because the work PC cannot accept inbound traffic. The work agent arrives by dialing out, so the broker must hold that backend connection and splice home-side streams onto it. This is the same broad pattern as reverse SSH tunnels, frp, or chisel, but purpose-built for RDP.

## Installation

### 1. Install Android Relay

Install `app-debug.apk` on the phone. For real use, produce and install a signed APK.

Open the app:

1. Enter the stable relay host or IPv6 address, or leave it blank until you tap `Use IPv6`.
2. Enter the relay port. The default is `8443`; choose another value from `1024` through `65535` only if your carrier and work proxy allow it.
3. If the phone has no stable hostname, tap `Detect IPv6`, confirm the shown public IPv6, then tap `Use IPv6`. This fills the relay host and shows the bracketed address the agent can use.
4. Enter certificate names. Include the stable hostname, or the raw IPv6 literal when using the detected public IPv6 path.
5. Optionally enter the work agent HTTP proxy, for example `http://proxy.example:8080`. This value is written into `agent.tnl` for the work PC only; the Android relay does not use the work proxy. Leave it blank for direct connections.
6. If using the hotspot path, confirm the hotspot raw-RDP allowlist. The app pre-fills this from the detected private IPv4 subnet, for example `192.168.203.0/24` when the phone address is `192.168.203.45`.
7. Tap `Generate bundles`.
8. Tap `Export agent` and share `agent.tnl` to the work PC.
9. Tap `Export client` and share `client.tnl` to the home PC.
10. Tap `Start relay`.
11. For the hotspot/private-LAN path, use the app's `Hotspot/LAN RDP address` value from the home PC.

### 2. Keep Android Alive

For reliability:

- Keep the phone plugged in.
- Set the app battery mode to unrestricted.
- Disable aggressive vendor battery killing or enable autostart/protected-app allowlisting.
- Leave `Start on boot` enabled.
- Use the optional VPN persistence mode only for phones that still kill the foreground service.

Do not enable Android VPN lockdown mode for the no-route persistence VPN. Lockdown can strand the phone's internet or hotspot if the persistence service is down.

### 3. Install Work Agent

Put these beside each other:

```text
agent-windows-amd64.exe
agent-installer-windows-amd64.exe
agent.tnl
```

Run `agent-installer-windows-amd64.exe`. It defaults to `D:\TunnelDesktop\Agent` when `D:` exists, copies the work agent as `agent.exe`, copies `agent.tnl`, installs or updates the `TunnelDesktopAgent` Windows service, configures SCM restart recovery, and starts the service.

Use `agent-configurator-windows-amd64.exe` later to change the installed bundle, start, stop, restart, uninstall, or run the agent self-test.

Check:

```powershell
.\agent-windows-amd64.exe -status
.\agent-windows-amd64.exe -self-test -bundle .\agent.tnl
```

### 4. Run Home Client

Import the bundle:

```powershell
.\client.exe -import .\client.tnl
```

Start `client.exe`, use the tray `Connect` item, and wait for Remote Desktop to open.

## Deliverables

### Android Relay App

The Android app is the normal relay and setup hub. It uses port `8443` by default because normal Android apps cannot bind privileged ports such as `443` without root. If you use a different port, choose a value from `1024` through `65535` and confirm the work proxy can CONNECT to that port.

It:

- Generates the CA, relay certificate, agent certificate, client certificate, shared token, and default settings.
- Stores the relay config in device-protected storage.
- Exports one opaque bundle per PC: `agent.tnl` and `client.tnl`.
- Runs the relay core through `relaycore.aar`.
- Hosts a `FOREGROUND_SERVICE_SPECIAL_USE` foreground service.
- Supports boot restart, locked-boot restart, watchdog alarm restart, partial WakeLock, WifiLock, and optional no-route `VpnService` persistence mode.
- Shows detected public IPv6 candidates and the bracketed relay address the work agent can use when there is no stable hostname.
- Shows detected private IPv4 candidates and the `IP:3389` address that a home PC can use on the hotspot/private LAN path.
- Pre-fills the hotspot raw-RDP allowlist from the selected private IPv4 network.
- Provides basic setup, status, log, and export UI.

### Work Agent

`agent.exe` is the work-side Windows component. It should run independent of user login because RDP must be reachable while the work PC is logged out.

Default behavior:

- `agent.exe` with no args looks for `agent.tnl` beside itself.
- It requests UAC elevation.
- It installs and starts the `TunnelDesktopAgent` Windows service.
- If service install is blocked, it attempts a current-user Scheduled Task fallback.

Debug/operations commands:

```powershell
.\agent.exe -console -bundle .\agent.tnl
.\agent.exe -self-test -bundle .\agent.tnl
.\agent.exe -status
.\agent.exe -uninstall
```

Use an explicit proxy address in `agent.tnl` when the work PC must egress through a proxy. A LocalSystem Windows service does not inherit the interactive user's WinINET/PAC settings. Leave the proxy blank during Android setup when the work agent should connect directly.

### Agent Installer And Configurator

`agent-installer-windows-amd64.exe` and `agent-configurator-windows-amd64.exe` are native Windows GUI builds from `cmd/agent-configurator`.

They:

- Prefer `D:\TunnelDesktop\Agent` as the install directory when `D:` exists.
- Validate that the selected `.tnl` bundle is an agent bundle.
- Copy the selected agent binary to `agent.exe` and bundle to `agent.tnl` in the install directory.
- Install or update the automatic `TunnelDesktopAgent` Windows service.
- Configure SCM restart recovery for the service.
- Start, stop, restart, uninstall, refresh status, open the install folder, and run `agent.exe -self-test`.

Service-changing actions request UAC elevation. Status refresh and self-test can run from the normal desktop session.

### Home Client

`client.exe` is the secure home-side internet path. It is a system tray helper.

It:

- Imports `client.tnl`.
- Starts a local loopback listener.
- Dials the phone relay with TLS/mTLS/token auth.
- Launches `mstsc` against the configured loopback port.

Import once:

```powershell
.\client.exe -import .\client.tnl
```

Then run `client.exe` normally and use the tray `Connect` item.

Debug mode:

```powershell
.\client.exe -console -bundle .\client.tnl
```

Hotspot users can skip `client.exe` and point stock `mstsc` at the Android app's displayed `Hotspot/LAN RDP address` if the Android raw-RDP listener is enabled and allowlisted.

### Dev Harness And VPS Fallback

`cmd/relay` and `tools/gencerts` are not part of the normal phone setup. They exist for:

- PC loopback/LAN testing.
- A tiny VPS fallback when cellular inbound IPv6 or proxy IPv6 egress fails.

## Security Model

- Public listener: TLS 1.3 minimum.
- Authentication: relay server cert plus client mTLS cert plus shared token.
- Role tag: authenticated sessions declare `agent` or `client` immediately after TLS.
- Fast reject: unauthenticated scanners cannot trigger a work-PC `127.0.0.1:3389` dial.
- Raw RDP: optional, intended only for hotspot/LAN, allowlisted by source IP/CIDR.
- Stream limits: relay caps concurrent home streams and TLS connections.
- Panic isolation: relay goroutines recover, which matters because Android `.aar` panics crash the app process.

The `.tnl` files contain private key material and bearer token material. Treat them as secrets. Do not commit them, paste them into tickets, or store them in shared locations.

This software can route around corporate egress controls to expose an internal RDP session. Confirm that use is permitted by workplace policy. This project intentionally does not add anti-monitoring, stealth, or obfuscation features.

## Phase 0 Feasibility

Do this before depending on the phone relay path.

1. Verify the phone accepts inbound TCP on cellular IPv6.

   Turn phone WiFi off. Start a temporary listener on the phone, for example with Termux:

   ```sh
   nc -6 -l -p 8443
   ```

   From an off-network host, connect to the phone's public IPv6 on port 8443.

2. Verify the corporate proxy can CONNECT to IPv6.

   From the work PC:

   ```powershell
   curl -x http://PROXY:PORT -v https://[PHONE_IPV6]:8443/
   ```

   A successful CONNECT followed by a TLS error or handshake attempt is enough to prove reachability. A `400`, `403`, `502`, or timeout means this phone path is not viable as-is.

If either check fails, run `cmd/relay` on a small public VPS and regenerate bundles using the VPS hostname as `relay_addr`.

## Build Prerequisites

Required:

- Go 1.25+.
- Android SDK for Android builds.
- Android NDK for `gomobile bind`.
- JDK 17+.
- Gradle, unless using a wrapper in `android/`.
- `gomobile` and `gobind`.

This repo's build scripts expect PowerShell on Windows. They also derive Gradle proxy settings from `HTTPS_PROXY` or `HTTP_PROXY` when present.

Useful environment variables:

```powershell
$env:ANDROID_HOME = 'D:\Android\Sdk'
$env:ANDROID_SDK_ROOT = 'D:\Android\Sdk'
$env:JAVA_HOME = 'D:\Scoop\apps\temurin17-jdk\current'
```

## Build Commands

Build Go binaries:

```powershell
.\build\build-go.ps1
```

Build the gomobile AAR:

```powershell
.\build\build-aar.ps1
```

Build the debug APK:

```powershell
.\build\build-android.ps1
```

Artifacts:

```text
dist/bin/agent-windows-amd64.exe
dist/bin/agent-installer-windows-amd64.exe
dist/bin/agent-configurator-windows-amd64.exe
dist/bin/client-windows-amd64.exe
dist/bin/relay-windows-amd64.exe
dist/bin/relay-linux-amd64
dist/bin/relay-linux-arm64
android/app/libs/relaycore.aar
android/app/build/outputs/apk/debug/app-debug.apk
```

## PC Harness Workflow

Generate test bundles:

```powershell
go run .\tools\gencerts `
  -out dist\certs `
  -relay-host localhost,127.0.0.1,::1 `
  -relay-addr localhost:8443 `
  -agent-proxy direct `
  -client-listen 127.0.0.1:3390
```

Run relay:

```powershell
.\dist\bin\relay-windows-amd64.exe `
  -config dist\certs\relay\config.json `
  -listen 127.0.0.1:8443
```

Run agent in console mode:

```powershell
.\dist\bin\agent-windows-amd64.exe `
  -console `
  -bundle dist\certs\agent\agent.tnl
```

Run client in console mode:

```powershell
.\dist\bin\client-windows-amd64.exe `
  -console `
  -bundle dist\certs\client\client.tnl
```

Point `mstsc` at `127.0.0.1:3390` if it does not auto-launch.

## Configuration And Bundle Format

`.tnl` bundles are base64url encoded JSON with a `tnl1.` prefix. They include:

- role: `agent` or `client`
- relay address
- TLS server name
- CA PEM
- role certificate PEM
- role private key PEM
- shared token
- role-specific settings such as proxy, local listen port, or RDP target

Relay config JSON can use either file paths:

```json
{
  "ca_file": "ca.crt",
  "cert_file": "relay.crt",
  "key_file": "relay.key"
}
```

or inline PEM fields:

```json
{
  "ca_pem": "...",
  "cert_pem": "...",
  "key_pem": "..."
}
```

The Android app uses inline PEM stored in device-protected app storage. The standalone relay harness can use either form.

## Troubleshooting

### Agent Service Does Not Start

Run:

```powershell
.\agent.exe -console -bundle .\agent.tnl
.\agent.exe -self-test -bundle .\agent.tnl
```

Common causes:

- `agent.tnl` is missing or unreadable.
- Proxy address is wrong.
- Corporate proxy cannot CONNECT to the relay hostname/port.
- Phone is not reachable on cellular IPv6.
- Local RDP is disabled or not listening on `127.0.0.1:3389`.

### Client Connects But RDP Fails

Check:

- Agent service is running.
- Android relay status shows an agent connection.
- Local client port is not already in use.
- Work PC allows RDP and the target account is permitted to log in remotely.

### Android Relay Stops

Check:

- Foreground notification is visible.
- Relay address uses an Android-allowed listener port such as `8443`; normal Android apps cannot bind `443`.
- If you change the relay port, tap `Generate bundles` again and re-export the updated agent/client bundles.
- Battery mode is unrestricted.
- OEM autostart/protected-app allowlisting is enabled.
- Phone is on charger.
- Watchdog is enabled by leaving `Start on boot` or `running` state active.
- Optional VPN persistence is enabled only if needed.

### Gradle Cannot Download Dependencies

Set proxy environment variables before running Android builds:

```powershell
$env:HTTPS_PROXY = 'http://proxy.example:8080'
$env:HTTP_PROXY = 'http://proxy.example:8080'
.\build\build-android.ps1
```

## Development

Run all Go tests:

```powershell
go test ./...
```

Rebuild after relaycore changes:

```powershell
.\build\build-aar.ps1
.\build\build-android.ps1
```

Rebuild after Windows CLI changes:

```powershell
.\build\build-go.ps1
```

The repository has no external deployment target. The deployable outputs are the files in `dist/bin` and the APK/AAR under `android/app`.

## Repository Layout

```text
cmd/agent          Windows service deliverable
cmd/agent-configurator Windows service installer/configurator GUI
cmd/client         Windows tray helper deliverable
cmd/relay          standalone harness / VPS fallback
internal/tunnel    TLS, auth, CONNECT, yamux, pipe, allowlist
internal/relaycore relay broker, identity generation, gomobile facade
mobile/relaycore   gomobile wrapper around internal relaycore
tools/gencerts     harness .tnl generator
android/           Android relay app
build/             build scripts
```

## Status

This repo currently contains:

- Android relay app and gomobile `relaycore.aar`.
- Windows work agent implemented as a Windows service deliverable.
- Windows installer/configurator GUI for the work agent service.
- Windows home client implemented as a tray helper deliverable.
- Standalone relay and bundle generator for PC testing and VPS fallback.
- Build scripts for Go binaries, Android AAR, and debug APK.

Phase 0 network feasibility still must be proven on the real phone/carrier/work-proxy path before relying on the phone-hosted relay.

## Current Limitations

- Phase 0 phone/carrier/proxy reachability cannot be automated from this repo.
- Android UI is intentionally functional and minimal; signed release packaging is not yet added.
- Public IPv6 display does not make the address stable. Regenerate and redistribute bundles if the carrier rotates the phone address and no DDNS hostname is used.
- The optional VPN persistence mode is a no-route persistence hook, not a traffic tunnel.
- QR export is planned but not currently implemented; bundles export through Android share text.
