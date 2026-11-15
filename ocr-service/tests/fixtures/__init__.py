"""测试夹具：用 Pillow 生成一张含账单文字的 PNG。

图上真实绘制文字（供真实模型识别），同时把同一文本写入 PNG tEXt 元数据
（键 ocr_text），使无模型的 mock 引擎能确定性提取 —— 这是 mock 引擎
"无模型可用"的测试通道（详见 app/engines/mock.py 模块说明）。
"""

from __future__ import annotations

from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

FIXTURE_TEXT = "微信支付 麦当劳 35.00元 2026-10-03 12:30:15"
# 本文件即位于 tests/fixtures/ 目录内，夹具直接生成在同目录
FIXTURE_DIR = Path(__file__).resolve().parent
FIXTURE_PATH = FIXTURE_DIR / "wechat_bill_fixture.png"

FONT_CANDIDATES = [
    "C:/Windows/Fonts/msyh.ttc",
    "C:/Windows/Fonts/simhei.ttf",
    "C:/Windows/Fonts/simsun.ttc",
    "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
    "/System/Library/Fonts/PingFang.ttc",
]


def _load_font(size: int = 32) -> ImageFont.FreeTypeFont | ImageFont.ImageFont | None:
    for candidate in FONT_CANDIDATES:
        if Path(candidate).exists():
            try:
                return ImageFont.truetype(candidate, size)
            except OSError:
                continue
    return ImageFont.load_default()


def make_fixture(path: Path | None = None) -> Path:
    """生成测试 PNG（800×260，白底黑字，含 tEXt 元数据 ocr_text）。"""
    target = path or FIXTURE_PATH
    target.parent.mkdir(parents=True, exist_ok=True)
    image = Image.new("RGB", (800, 260), color=(245, 245, 245))
    draw = ImageDraw.Draw(image)
    font = _load_font(32)
    small = _load_font(24)
    lines = [
        "微信支付",
        FIXTURE_TEXT,
        "支出 · 餐饮",
        "支付成功",
    ]
    y = 24
    for index, line in enumerate(lines):
        current_font = font if index != 3 else small
        draw.text((40, y), line, fill=(30, 30, 30), font=current_font)
        y += 56
    # 写入 tEXt 元数据：mock 引擎的确定性文本来源
    from PIL.PngImagePlugin import PngInfo

    meta = PngInfo()
    meta.add_text("ocr_text", FIXTURE_TEXT)
    image.save(target, format="PNG", pnginfo=meta)
    return target


def ensure_fixture() -> Path:
    """夹具不存在时生成，返回其路径。"""
    if not FIXTURE_PATH.exists():
        make_fixture()
    return FIXTURE_PATH
