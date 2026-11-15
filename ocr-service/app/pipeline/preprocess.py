"""预处理（ARCHITECTURE §8.3）：

PIL 打开 → 读取 PNG 文本元数据（mock 引擎确定性来源）→ EXIF 转正 →
长边 > 2048 等比缩放 → 转 RGB → JPEG q=88 bytes。

安全边界：
- 仅按绝对路径读取已存在的图片文件，不写任何文件；
- 大小上限 OCR_MAX_IMAGE_BYTES（默认 20MB）；
- 扩展名白名单 png/jpg/jpeg/webp/bmp（防误读任意文件）。
"""

from __future__ import annotations

import base64
import binascii
import io
import os

from PIL import Image, ImageOps

ALLOWED_EXTENSIONS = frozenset({".png", ".jpg", ".jpeg", ".webp", ".bmp"})
EXIF_ORIENTATIONS = frozenset({2, 3, 4, 5, 6, 7, 8})


class ImageLoadError(ValueError):
    """图片加载失败（路径非法 / 文件不存在 / 非图片 / 超限）。"""


def validate_image_path(path: str) -> str:
    """校验图片绝对路径：必须存在、扩展名合法、大小不超限。返回规范化路径。"""
    if not path or not os.path.isabs(path):
        raise ImageLoadError(f"image_path 必须为绝对路径：{path!r}")
    normalized = os.path.normpath(path)
    if not os.path.exists(normalized):
        raise ImageLoadError(f"图片文件不存在：{normalized}")
    if not os.path.isfile(normalized):
        raise ImageLoadError(f"路径不是普通文件：{normalized}")
    ext = os.path.splitext(normalized)[1].lower()
    if ext not in ALLOWED_EXTENSIONS:
        raise ImageLoadError(f"不支持的图片扩展名 {ext}（允许：{sorted(ALLOWED_EXTENSIONS)}）")
    size = os.path.getsize(normalized)
    from app.config import get_settings

    if size > get_settings().max_image_bytes:
        raise ImageLoadError(f"图片超过大小上限 {get_settings().max_image_bytes} 字节（当前 {size}）")
    return normalized


def decode_base64_image(b64: str) -> Image.Image:
    """解码 base64 图片（容忍 data:image/...;base64, 前缀）。"""
    payload = b64.strip()
    if payload.startswith("data:") and "base64," in payload:
        payload = payload.split("base64,", 1)[1]
    try:
        raw = base64.b64decode(payload, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise ImageLoadError(f"image_base64 解码失败：{exc}") from exc
    try:
        return Image.open(io.BytesIO(raw))
    except Exception as exc:  # noqa: BLE001 —— PIL 对任意字节都可能抛
        raise ImageLoadError(f"base64 内容不是有效图片：{exc}") from exc


def extract_png_text(image: Image.Image) -> str:
    """读取 PNG tEXt/iTXt 元数据（Pillow 9+ 的 Image.text 属性）。"""
    text_meta = getattr(image, "text", None)
    if isinstance(text_meta, dict):
        for key in ("ocr_text", "Description", "description", "Comment", "comment"):
            value = text_meta.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
    return ""


def load_image(path: str | None = None, b64: str | None = None) -> Image.Image:
    """加载图片（路径或 base64 二选一），返回原始 Image（保留元数据）。"""
    if path:
        normalized = validate_image_path(path)
        try:
            image = Image.open(normalized)
        except Exception as exc:  # noqa: BLE001
            raise ImageLoadError(f"图片打开失败：{exc}") from exc
        image.filename = normalized
        return image
    if b64:
        return decode_base64_image(b64)
    raise ImageLoadError("image_path 与 image_base64 必须提供其一")


def preprocess(image: Image.Image) -> Image.Image:
    """EXIF 转正 → 缩放 → RGB；并把文本元数据转移到 ocr_text 属性。"""
    from app.config import get_settings

    raw_text = extract_png_text(image)
    try:
        orientation = image.getexif().get(274)
    except Exception:  # noqa: BLE001 —— 无 EXIF 或读取失败均可忽略
        orientation = None
    if orientation in EXIF_ORIENTATIONS:
        image = ImageOps.exif_transpose(image)
    settings = get_settings()
    if max(image.size) > settings.max_side:
        ratio = settings.max_side / max(image.size)
        new_size = (max(1, round(image.width * ratio)), max(1, round(image.height * ratio)))
        image = image.resize(new_size, Image.LANCZOS)
    if image.mode != "RGB":
        image = image.convert("RGB")
    # 保留文件名与文本元数据（convert/resize 会丢失自定义属性）
    image.ocr_text = raw_text  # type: ignore[attr-defined]
    if not getattr(image, "filename", None):
        image.filename = ""  # type: ignore[attr-defined]
    return image


def image_to_jpeg_bytes(image: Image.Image) -> bytes:
    """转 JPEG bytes（真实模型输入）。"""
    from app.config import get_settings

    rgb = image if image.mode == "RGB" else image.convert("RGB")
    buffer = io.BytesIO()
    rgb.save(buffer, format="JPEG", quality=get_settings().jpeg_quality)
    return buffer.getvalue()
