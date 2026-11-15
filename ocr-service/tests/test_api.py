"""API 集成单测：healthz/readyz、sync/async 识别、任务轮询、模型热切换。

注意：整个模块共享一个 TestClient 上下文（单事件循环）——
registry 锁与识别队列都是进程级单例，跨事件循环复用会报
"got Future attached to a different loop"。
"""

from __future__ import annotations

from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from app.main import app
from tests.fixtures import FIXTURE_TEXT, ensure_fixture


@pytest.fixture(scope="module")
def client() -> TestClient:
    with TestClient(app) as c:
        yield c


@pytest.fixture(scope="module")
def fixture_path() -> Path:
    return ensure_fixture()


class TestHealth:
    def test_healthz(self, client: TestClient) -> None:
        response = client.get("/healthz")
        assert response.status_code == 200
        assert response.json()["status"] == "ok"

    def test_readyz(self, client: TestClient) -> None:
        response = client.get("/readyz")
        assert response.status_code == 200
        body = response.json()
        assert body["ready"] is True
        assert body["engine"] == "mock"


class TestRecognizeSync:
    def test_sync_with_fixture(self, client: TestClient, fixture_path: Path) -> None:
        response = client.post(
            "/api/v1/recognize",
            json={"image_path": str(fixture_path), "model": "mock", "mode": "sync", "hint": "wechat"},
        )
        assert response.status_code == 200
        body = response.json()
        assert body["success"] is True
        assert body["model_used"] == "mock"
        assert isinstance(body["elapsed_ms"], int)
        result = body["data"]
        # PRD 7.3 字段齐备
        for key in (
            "id", "source", "timestamp", "account", "transaction_type", "amount",
            "category", "sub_category", "merchant", "region", "tags", "remark",
            "confidence", "raw_text", "model_used", "need_review",
        ):
            assert key in result, f"缺少 PRD 7.3 字段 {key}"
        # §5.8 契约字段
        for key in ("transaction_time", "amount_cents", "account_id", "category_id", "warnings"):
            assert key in result, f"缺少 §5.8 契约字段 {key}"
        assert result["amount"] == 35.0
        assert result["amount_cents"] == 3500
        assert result["transaction_type"] == "expense"
        assert result["transaction_time"] == "2026-10-03 12:30:15"
        assert result["merchant"] == "麦当劳"
        assert result["need_review"] is True
        assert FIXTURE_TEXT in result["raw_text"]
        assert any("启发式降级" in warning for warning in result["warnings"])

    def test_sync_with_base64(self, client: TestClient, fixture_path: Path) -> None:
        import base64

        b64 = base64.b64encode(fixture_path.read_bytes()).decode("ascii")
        response = client.post(
            "/api/v1/recognize",
            json={"image_base64": b64, "model": "auto", "mode": "sync"},
        )
        assert response.status_code == 200
        assert response.json()["data"]["amount"] == 35.0

    def test_missing_image_422(self, client: TestClient) -> None:
        response = client.post("/api/v1/recognize", json={"model": "mock", "mode": "sync"})
        assert response.status_code == 422  # pydantic 校验

    def test_nonexistent_path_400(self, client: TestClient) -> None:
        response = client.post(
            "/api/v1/recognize",
            json={"image_path": "D:/不存在/xxx.png", "mode": "sync"},
        )
        assert response.status_code == 400
        assert response.json()["error"]["code"] == "INVALID_IMAGE"

    def test_relative_path_400(self, client: TestClient) -> None:
        response = client.post(
            "/api/v1/recognize",
            json={"image_path": "relative/x.png", "mode": "sync"},
        )
        assert response.status_code == 400


class TestRecognizeAsync:
    def test_async_task_lifecycle(self, client: TestClient, fixture_path: Path) -> None:
        response = client.post(
            "/api/v1/recognize",
            json={"image_path": str(fixture_path), "model": "mock", "mode": "async", "hint": "wechat"},
        )
        assert response.status_code == 202
        body = response.json()
        assert body["success"] is True
        assert body["task_id"].startswith("ocr_")

        # 轮询直到 done（worker 并发度 1，mock 极快）
        final = None
        for _ in range(50):
            task = client.get(f"/api/v1/tasks/{body['task_id']}")
            assert task.status_code == 200
            view = task.json()
            assert view["status"] in ("pending", "processing", "done", "failed")
            if view["status"] == "done":
                final = view
                break
        assert final is not None, "任务未在预期时间内完成"
        assert final["progress"] == 100
        assert final["result"]["amount_cents"] == 3500
        assert final["model_used"] == "mock"
        assert isinstance(final["elapsed_ms"], int)

    def test_unknown_task_404(self, client: TestClient) -> None:
        response = client.get("/api/v1/tasks/ocr_nonexistent")
        assert response.status_code == 404

    def test_batch(self, client: TestClient, fixture_path: Path) -> None:
        response = client.post(
            "/api/v1/recognize/batch",
            json={"images": [str(fixture_path), {"image_path": str(fixture_path), "hint": "wechat"}], "model": "mock"},
        )
        assert response.status_code == 202
        body = response.json()
        assert len(body["task_ids"]) == 2
        # 兼容路径
        response_alias = client.post(
            "/api/v1/recognize-batch",
            json={"images": [str(fixture_path)], "model": "mock"},
        )
        assert response_alias.status_code == 202

    def test_failed_task_invalid_image(self, client: TestClient) -> None:
        response = client.post(
            "/api/v1/recognize",
            json={"image_path": "D:/不存在/bad.png", "mode": "async"},
        )
        assert response.status_code == 202
        task_id = response.json()["task_id"]
        final = None
        for _ in range(50):
            task = client.get(f"/api/v1/tasks/{task_id}")
            view = task.json()
            if view["status"] in ("done", "failed"):
                final = view
                break
        assert final is not None
        assert final["status"] == "failed"
        assert "INVALID_IMAGE" in (final["error"] or "")


class TestModel:
    def test_get_model(self, client: TestClient) -> None:
        response = client.get("/api/v1/model")
        assert response.status_code == 200
        body = response.json()
        assert body["current"] == "mock"
        assert body["healthy"] is True
        assert {"count", "avg_ms", "p95_ms", "fail_rate"} <= set(body["stats"].keys())
        names = [item["name"] for item in body["available"]]
        assert "mock" in names

    def test_switch_to_mock(self, client: TestClient) -> None:
        response = client.post("/api/v1/model/switch", json={"name": "mock"})
        assert response.status_code == 200
        body = response.json()
        assert body["ok"] is True
        assert body["current"] == "mock"

    def test_switch_to_nonexistent_model_keeps_mock(self, client: TestClient, fixture_path: Path) -> None:
        response = client.post("/api/v1/model/switch", json={"name": "ollama:nonexistent-model-xyz"})
        # 端点不可达或模型不存在 → ok:false，当前引擎保持 mock
        assert response.status_code == 200
        body = response.json()
        assert body["ok"] is False
        assert body["current"] == "mock"
        assert body["error"]
        # mock 仍可用
        recognize = client.post(
            "/api/v1/recognize",
            json={"image_path": str(fixture_path), "mode": "sync"},
        )
        assert recognize.status_code == 200
        assert recognize.json()["data"]["amount"] == 35.0

    def test_switch_unknown_name(self, client: TestClient) -> None:
        response = client.post("/api/v1/model/switch", json={"name": "no-such-engine"})
        assert response.status_code == 200
        assert response.json()["ok"] is False
