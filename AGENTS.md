# AGENTS.md

## Project Overview

TunnelDesktop is a Go + Android project for an RDP rendezvous tunnel. The normal relay is the Android app. The work PC runs `agent.exe` as a Windows service installed/configured by the Windows setup GUI. The home PC runs `client.exe` as a tray helper. `cmd/relay` and `tools/gencerts` are dev harness / VPS fallback utilities, not the normal deployment path.

## Architecture Rules

- The Android app is the normal relay and setup hub.
- `internal/relaycore` owns relay broker logic and identity generation.
- `mobile/relaycore` is only the gomobile-safe wrapper around `internal/relaycore`.
- `cmd/agent` must remain Windows-service-first. Console mode is debug-only.
- `cmd/agent-configurator` owns the native Windows installer/configurator GUI for the work agent service.
- `cmd/client` must remain bundle/tray-first. Console mode is debug-only.
- `.tnl` bundles are the primary user-facing configuration artifact.
- Do not make users hand-copy PEM files for the normal setup path.
- Keep raw RDP default-deny and hotspot/LAN allowlisted.
- Do not add stealth, anti-monitoring, or obfuscation behavior.

## Sensitive Files

`.tnl` files, generated cert directories, relay configs with inline PEM, and Android device-protected relay configs contain private keys or bearer tokens. Do not commit generated bundles, generated certs, logs containing bundles, or screenshots that expose bundle text.

The current `.gitignore` excludes `dist/`, Android build output, APKs, AARs, and local Android properties. Preserve that behavior.

## Build Commands

Run Go tests:

```powershell
go test ./...
```

Build Windows/Linux Go artifacts:

```powershell
.\build\build-go.ps1
```

Build gomobile AAR:

```powershell
.\build\build-aar.ps1
```

Build Android debug APK:

```powershell
.\build\build-android.ps1
```

When `internal/relaycore` or `mobile/relaycore` changes, rebuild the AAR and APK. When `cmd/agent`, `cmd/agent-configurator`, `cmd/client`, `cmd/relay`, `internal/tunnel`, or shared bundle types change, run `go test ./...` and `.\build\build-go.ps1`.

## Tooling Notes

This repo has been built with:

- Go 1.26.x installed under `D:\Scoop`
- JDK under `D:\Scoop\apps\temurin17-jdk\current`
- Android SDK/NDK under `D:\Android\Sdk`
- gomobile/gobind under `D:\Go\bin`
- rsrc under `D:\Go\bin` for Windows GUI manifest resources
- Gradle installed through Scoop

`build-android.ps1` derives Gradle proxy settings from `HTTPS_PROXY` or `HTTP_PROXY`.

## Deployment

There is no configured external deploy target for this repo. For this project, "deployable artifacts" means:

- `dist/bin/agent-windows-amd64.exe`
- `dist/bin/agent-installer-windows-amd64.exe`
- `dist/bin/agent-configurator-windows-amd64.exe`
- `dist/bin/client-windows-amd64.exe`
- `dist/bin/relay-windows-amd64.exe`
- `dist/bin/relay-linux-amd64`
- `dist/bin/relay-linux-arm64`
- `android/app/libs/relaycore.aar`
- `android/app/build/outputs/apk/debug/app-debug.apk`

After code changes, build the relevant artifacts. Do not claim an external deployment was performed unless a real deployment target is added.

## Verification Expectations

For documentation-only changes, read the modified files back or run a lightweight command if useful.

For Go changes, run:

```powershell
go test ./...
```

For release-impacting Go changes, also run:

```powershell
.\build\build-go.ps1
```

For Android or relaycore gomobile changes, run:

```powershell
.\build\build-aar.ps1
.\build\build-android.ps1
```

## Phase 0 Constraint

Do not present the phone relay path as proven unless the user has verified both:

- cellular IPv6 inbound TCP to the phone works
- the corporate proxy can CONNECT to the phone hostname or IPv6 on the selected relay port, default `8443`

If either fails, the supported fallback is a public VPS running `cmd/relay`, with regenerated `.tnl` bundles pointing at that VPS.

## Coding Guidance

- Prefer existing package boundaries and helper APIs.
- Keep `internal/tunnel` focused on protocol primitives: TLS, auth, proxy CONNECT, yamux config, byte piping, allowlist.
- Keep identity and bundle generation in `internal/relaycore`, not duplicated in CLIs or Kotlin.
- Keep Android Kotlin thin; core relay behavior should stay in Go.
- Use `apply_patch` for manual file edits.
- Avoid committing generated private key material under `dist/` or elsewhere.
