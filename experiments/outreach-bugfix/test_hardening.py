"""Production-hardening task (the 'decent bug' for the loop-vs-graph experiment).

Five independent requirements across four modules. Each is a real gap in the
current code. The task for an agent (loop or graph) is: make the WHOLE suite green
without breaking the existing 18 tests.

Independent fix targets (so a graph can fan out one node per file):
  1. app/schemas/chat.py     - validate chat message (non-empty, max length)
  2. app/api/v1/chat.py       - ownership check on POST chat (no IDOR / no 404 today)
  3. app/api/v1/campaigns.py  - pagination (limit / offset) on GET /campaigns
  (plus the two pre-existing bugs in app/services/chat_service.py et al.)
"""
import pytest


# --- Requirement A: input validation on chat messages (app/schemas/chat.py) ---

async def test_chat_rejects_empty_message(client):
    cid = (await client.post("/api/v1/campaigns", json={"type": "b2b"})).json()["campaign_id"]
    resp = await client.post(f"/api/v1/campaigns/{cid}/chat", json={"message": ""})
    assert resp.status_code == 422, "empty chat message must be rejected with 422"


async def test_chat_rejects_oversized_message(client):
    cid = (await client.post("/api/v1/campaigns", json={"type": "b2b"})).json()["campaign_id"]
    resp = await client.post(f"/api/v1/campaigns/{cid}/chat", json={"message": "x" * 8000})
    assert resp.status_code == 422, "oversized chat message must be rejected with 422"


# --- Requirement B: ownership / existence check on POST chat (app/api/v1/chat.py) ---

async def test_chat_on_unknown_campaign_returns_404(client):
    missing = "00000000-0000-0000-0000-000000000000"
    resp = await client.post(f"/api/v1/campaigns/{missing}/chat", json={"message": "hello"})
    assert resp.status_code == 404, "posting chat to a campaign you don't own must be 404, not a 200 stream"


# --- Requirement C: pagination on GET /campaigns (app/api/v1/campaigns.py) ---

async def test_list_campaigns_respects_limit(client):
    for _ in range(3):
        await client.post("/api/v1/campaigns", json={"type": "b2b"})
    data = (await client.get("/api/v1/campaigns?limit=2")).json()
    assert len(data["campaigns"]) == 2, "limit must cap the returned page size"
    assert data["total"] == 3, "total must still report the full count"


async def test_list_campaigns_respects_offset(client):
    for _ in range(3):
        await client.post("/api/v1/campaigns", json={"type": "b2b"})
    page1 = (await client.get("/api/v1/campaigns?limit=2&offset=0")).json()["campaigns"]
    page2 = (await client.get("/api/v1/campaigns?limit=2&offset=2")).json()["campaigns"]
    assert len(page1) == 2 and len(page2) == 1, "offset must page through results"
