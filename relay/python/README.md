# DeskFerry Python Relay

This is a Python/FastAPI implementation of the DeskFerry Azure WebSocket relay.

It matches the .NET relay contract:

- `GET /relay/` and `GET /relay/<room>` dashboard UI.
- `GET /relay/health` health JSON.
- `GET /relay/status?room=<room>` status JSON.
- `GET /relay/ws` and `GET /relay/<room>/ws` WebSocket endpoints.
- WebSocket roles through `X-DeskFerry-Role`: `agent`, `client`, `home-agent`, `probe`, and `dashboard`.

Run locally:

```powershell
python -m pip install -r relay\python\requirements.txt
python -m uvicorn app:app --app-dir relay\python --host 127.0.0.1 --port 8000
```

Use this relay URL for a local room:

```text
http://127.0.0.1:8000/relay/workdesk
```

Build the deployable zips:

```powershell
python -m pip install -r relay\python\requirements-dev.txt
.\build\build-python-relay.ps1
```

The build writes:

```text
dist\python-relay\deskferry-python-relay.zip
dist\python-relay\deskferry-python-relay-linux-cp39-vendored.zip
```

The vendored zip targets Oracle Linux 9's Python 3.9 runtime and includes FastAPI, Uvicorn, and compatibility backports under `vendor/`.

## OCI VM Deployment

The current OCI VM relay is deployed at:

```text
http://217.142.228.117/relay/b
```

This deployment listens on plain HTTP port `80`. Configure clients with `http://217.142.228.117/relay/<room>`, not `https://217.142.228.117/relay/<room>`, because the VM does not serve the relay on port `443`.

The VM runs the relay directly on port `80` as:

```text
deskferry-relay.service
```

Service layout:

```text
/opt/deskferry/python-relay/app.py
/opt/deskferry/python-relay/vendor/
/etc/systemd/system/deskferry-relay.service
```

The systemd service uses:

```text
PYTHONPATH=/opt/deskferry/python-relay/vendor
/usr/bin/python3 -m uvicorn app:app --host 0.0.0.0 --port 80 --proxy-headers
```

Useful checks on the VM:

```sh
systemctl status deskferry-relay
curl -fsS http://127.0.0.1/relay/health
curl -fsS 'http://127.0.0.1/relay/status?room=b'
```

The OCI network and guest firewall must allow inbound TCP `80`. SSH access from this workstation used HTTP CONNECT through `192.9.200.25:3128`.
