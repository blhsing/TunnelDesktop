import json

from fastapi.testclient import TestClient

from app import app, room_id


def test_room_id_matches_dotnet_normalization():
    assert room_id("") == "default"
    assert room_id(" WorkDesk ") == "workdesk"
    assert room_id("/Team Room!!/") == "team-room"
    assert room_id("...") == "default"
    assert room_id("A" * 80) == "a" * 64


def test_health_and_empty_status():
    client = TestClient(app)

    health = client.get("/relay/health")
    assert health.status_code == 200
    assert health.json()["service"] == "DeskFerry.Relay"

    status = client.get("/relay/status?room=unit-empty")
    assert status.status_code == 200
    body = status.json()
    assert body["service"] == "DeskFerry.Relay"
    assert body["rooms"] == []


def test_icon_endpoint():
    client = TestClient(app)

    response = client.get("/relay/icon.svg")
    assert response.status_code == 200
    assert response.headers["content-type"].startswith("image/svg+xml")
    assert "<svg" in response.text


def test_home_agent_status_presence():
    client = TestClient(app)
    headers = {"X-DeskFerry-Role": "home-agent"}

    with client.websocket_connect("/relay/unit-home/ws", headers=headers):
        status = client.get("/relay/status?room=unit-home").json()
        assert status["rooms"][0]["home_agent_connected"] is True

    status = client.get("/relay/status?room=unit-home").json()
    assert status["rooms"][0]["home_agent_connected"] is False


def test_legacy_role_header_is_still_accepted():
    client = TestClient(app)
    headers = {"X-TunnelDesktop-Role": "probe"}

    with client.websocket_connect("/relay/unit-legacy/ws", headers=headers):
        pass


def test_agent_client_pair_and_bridge_bytes():
    client = TestClient(app)
    agent_headers = {"X-DeskFerry-Role": "agent"}
    client_headers = {"X-DeskFerry-Role": "client"}

    with client.websocket_connect("/relay/unit-bridge/ws", headers=agent_headers) as agent:
        with client.websocket_connect("/relay/unit-bridge/ws", headers=client_headers) as home:
            assert agent.receive_text() == "start"
            assert home.receive_text() == "start"

            home.send_bytes(b"from-home")
            assert agent.receive_bytes() == b"from-home"

            agent.send_bytes(b"from-agent")
            assert home.receive_bytes() == b"from-agent"

            status = client.get("/relay/status?room=unit-bridge").json()
            assert status["rooms"][0]["active_pairs"] == 1
            assert status["rooms"][0]["total_pairs"] == 1


def test_dashboard_websocket_receives_snapshot():
    client = TestClient(app)

    with client.websocket_connect("/relay/unit-dashboard/ws?role=dashboard") as dashboard:
        payload = json.loads(dashboard.receive_text())
        assert payload["service"] == "DeskFerry.Relay"
        assert payload["rooms"] == []
