import base64
import os
import time

import cv2
import numpy as np

from app.config import get_settings
from app.schemas import (
    BoundingBox,
    DetectFacesResponse,
    DetectedFace,
    FACE_QUALITY_MODEL_VERSION,
    FACE_QUALITY_RULE_VERSION,
    FaceQualityEvidence,
    QUALITY_REASON_BAD_GEOMETRY,
    QUALITY_REASON_BLURRED,
    QUALITY_REASON_EXTREME_POSE,
    QUALITY_REASON_INVALID_LANDMARKS,
    QUALITY_REASON_OCCLUDED,
    QUALITY_REASON_OVEREXPOSED,
    QUALITY_REASON_POSE_UNCERTAIN,
    QUALITY_REASON_TOO_SMALL,
    QUALITY_REASON_UNDEREXPOSED,
    ScoreKnownFaceResult,
    ScoreKnownFacesResponse,
)
# 质检硬阈值（与 rule_version v1 绑定）。改动这些阈值须递增 FACE_QUALITY_RULE_VERSION。
_MIN_FACE_PIXELS = 32  # 低于此像素尺寸直接判 too_small
_SHARPNESS_BLUR_THRESHOLD = 80.0  # Laplacian 方差低于此判 blurred
_BRIGHTNESS_OVEREXPOSED = 235.0
_BRIGHTNESS_UNDEREXPOSED = 20.0
_LANDMARK_COMPLETENESS_LOW = 0.6  # 关键点完整度低于此判 invalid_landmarks
_LANDMARK_GEOMETRY_LOW = 0.5  # 几何合理性低于此判 bad_geometry
_EXTREME_POSE_YAW = 75.0  # 偏航角绝对值超过此判 extreme_pose
_FACE_VALIDITY_NON_FACE = 0.35  # 二次人脸验证低于此为高确定性非人脸
_FACE_VALIDITY_UNCERTAIN = 0.6  # 介于非人脸阈值与此值之间为灰区

# 已知框重评分：目标框与检测框的一对一最高 IoU 匹配阈值，与后端 exclusionIoUThreshold=0.3 一致。
_SCORE_KNOWN_FACES_IOU_THRESHOLD = 0.3


class FaceDetector:
    def __init__(self) -> None:
        settings = get_settings()
        self.settings = settings
        self.embedding_size = settings.embedding_size
        self.default_confidence = settings.default_confidence
        self.app = self._init_insightface()

    def detect(
        self,
        *,
        image_path: str | None = None,
        image_base64: str | None = None,
        min_confidence: float = 0.5,
        max_faces: int = 20,
    ) -> DetectFacesResponse:
        started_at = time.perf_counter()
        frame = self._load_image(image_path=image_path, image_base64=image_base64)

        faces = []
        if frame is not None and max_faces > 0:
            faces = self._detect_faces(frame, min_confidence, max_faces)

        elapsed_ms = int((time.perf_counter() - started_at) * 1000)
        return DetectFacesResponse(
            faces=faces,
            processing_time_ms=max(elapsed_ms, 0),
            rule_version=FACE_QUALITY_RULE_VERSION,
            model_version=FACE_QUALITY_MODEL_VERSION,
        )

    def score_known_faces(
        self,
        *,
        image_base64: str,
        targets: list,
    ) -> ScoreKnownFacesResponse:
        """对一组已知目标框在同一张展示图上重评分。

        流程：
        1. 加载已旋转校正的展示图（仅 base64，不接受原始文件路径，避免坐标系错配）；
        2. 在该图上运行 InsightFace 检测，复用 _build_evidence 生成证据；
        3. 把检测框与目标框做一对一最高 IoU 匹配（阈值 _SCORE_KNOWN_FACES_IOU_THRESHOLD）；
        4. 命中的目标返回 matched + 证据；未命中返回 unmatched；图像读取/推理异常返回 error。

        绝不把 unmatched / 读图失败 / 推理异常当作 non_face——这些只能进入可重试技术状态。
        """
        started_at = time.perf_counter()
        results: list[ScoreKnownFaceResult] = []

        frame = self._load_image(image_path=None, image_base64=image_base64)

        # 目标顺序固定；逐个初始化为 unmatched，命中后覆盖。
        target_results: list[ScoreKnownFaceResult] = [
            ScoreKnownFaceResult(face_id=int(t.face_id), status="unmatched") for t in targets
        ]

        # 图像读取失败：所有目标标 error（可重试），不伪装判定。
        if frame is None:
            for i in range(len(target_results)):
                target_results[i] = ScoreKnownFaceResult(
                    face_id=target_results[i].face_id, status="error"
                )
            _ = int((time.perf_counter() - started_at) * 1000)
            return ScoreKnownFacesResponse(
                results=target_results,
                rule_version=FACE_QUALITY_RULE_VERSION,
                model_version=FACE_QUALITY_MODEL_VERSION,
            )

        frame_height, frame_width = frame.shape[:2]
        if frame_width == 0 or frame_height == 0 or not targets:
            return ScoreKnownFacesResponse(
                results=target_results,
                rule_version=FACE_QUALITY_RULE_VERSION,
                model_version=FACE_QUALITY_MODEL_VERSION,
            )

        # 运行检测。推理异常 → 所有目标 error。
        try:
            detected = self.app.get(frame)
        except Exception:
            for i in range(len(target_results)):
                target_results[i] = ScoreKnownFaceResult(
                    face_id=target_results[i].face_id, status="error"
                )
            return ScoreKnownFacesResponse(
                results=target_results,
                rule_version=FACE_QUALITY_RULE_VERSION,
                model_version=FACE_QUALITY_MODEL_VERSION,
            )

        if not detected:
            return ScoreKnownFacesResponse(
                results=target_results,
                rule_version=FACE_QUALITY_RULE_VERSION,
                model_version=FACE_QUALITY_MODEL_VERSION,
            )

        detected = sorted(detected, key=lambda f: float(f.det_score), reverse=True)

        # 预计算每个检测框的归一化 bbox + 像素裁剪坐标 + 证据。
        det_entries: list[dict] = []
        for face_obj in detected:
            score = float(face_obj.det_score)
            x1, y1, x2, y2 = face_obj.bbox.astype(int)
            x1 = max(0, x1)
            y1 = max(0, y1)
            x2 = min(frame_width, x2)
            y2 = min(frame_height, y2)
            width = x2 - x1
            height = y2 - y1
            if width <= 0 or height <= 0:
                continue
            bbox = BoundingBox(
                x=round(x1 / frame_width, 6),
                y=round(y1 / frame_height, 6),
                width=round(width / frame_width, 6),
                height=round(height / frame_height, 6),
            )
            evidence = self._build_evidence(
                frame, face_obj, x1, y1, width, height, frame_width, frame_height, score
            )
            quality = self._estimate_quality(
                frame, x1, y1, width, height, frame_width, frame_height, score
            )
            det_entries.append(
                {
                    "bbox": bbox,
                    "evidence": evidence,
                    "quality": quality,
                    "used": False,
                }
            )

        # 一对一最高 IoU 匹配：目标按原顺序，每个检测框最多被一个目标占用。
        used_det = [False] * len(det_entries)
        for idx, target in enumerate(targets):
            best_iou = _SCORE_KNOWN_FACES_IOU_THRESHOLD
            best_det = -1
            tb = target.bbox
            for di, entry in enumerate(det_entries):
                if used_det[di]:
                    continue
                iou = _bbox_iou(
                    tb.x, tb.y, tb.width, tb.height,
                    entry["bbox"].x, entry["bbox"].y, entry["bbox"].width, entry["bbox"].height,
                )
                if iou > best_iou:
                    best_iou = iou
                    best_det = di
            if best_det >= 0:
                used_det[best_det] = True
                entry = det_entries[best_det]
                target_results[idx] = ScoreKnownFaceResult(
                    face_id=int(target.face_id),
                    status="matched",
                    matched_iou=round(best_iou, 6),
                    evidence=entry["evidence"],
                    quality_score=round(float(entry["quality"]), 6),
                )

        return ScoreKnownFacesResponse(
            results=target_results,
            rule_version=FACE_QUALITY_RULE_VERSION,
            model_version=FACE_QUALITY_MODEL_VERSION,
        )

    def _load_image(self, *, image_path: str | None, image_base64: str | None) -> np.ndarray | None:
        if image_base64:
            try:
                payload = image_base64.split(",", 1)[-1]
                raw = base64.b64decode(payload)
                buffer = np.frombuffer(raw, dtype=np.uint8)
                frame = cv2.imdecode(buffer, cv2.IMREAD_COLOR)
                if frame is not None:
                    return frame
            except Exception:
                pass

        if image_path:
            frame = cv2.imread(image_path)
            if frame is None:
                raise FileNotFoundError(f"image not found or unreadable: {image_path}")
            return frame

        return None

    def _detect_faces(self, frame: np.ndarray, min_confidence: float, max_faces: int) -> list[DetectedFace]:
        frame_height, frame_width = frame.shape[:2]
        if frame_width == 0 or frame_height == 0:
            return []

        try:
            detected = self.app.get(frame)
        except Exception:
            return []

        if not detected:
            return []

        detected = sorted(detected, key=lambda f: float(f.det_score), reverse=True)

        faces = []
        for face_obj in detected:
            score = float(face_obj.det_score)
            if score < min_confidence:
                continue

            x1, y1, x2, y2 = face_obj.bbox.astype(int)
            x1 = max(0, x1)
            y1 = max(0, y1)
            x2 = min(frame_width, x2)
            y2 = min(frame_height, y2)
            width = x2 - x1
            height = y2 - y1
            if width <= 0 or height <= 0:
                continue

            bbox = BoundingBox(
                x=round(x1 / frame_width, 6),
                y=round(y1 / frame_height, 6),
                width=round(width / frame_width, 6),
                height=round(height / frame_height, 6),
            )

            embedding = self._extract_embedding(face_obj)
            evidence = self._build_evidence(frame, face_obj, x1, y1, width, height, frame_width, frame_height, score)
            quality = self._estimate_quality(frame, x1, y1, width, height, frame_width, frame_height, score)

            faces.append(
                DetectedFace(
                    bbox=bbox,
                    confidence=round(score, 6),
                    quality_score=quality,
                    embedding=embedding,
                    evidence=evidence,
                )
            )
            if len(faces) >= max_faces:
                break

        return faces

    def _extract_embedding(self, face_obj) -> list[float]:
        emb = face_obj.normed_embedding
        if emb is None:
            return [0.0] * self.embedding_size
        result = emb.tolist()
        if len(result) < self.embedding_size:
            result.extend([0.0] * (self.embedding_size - len(result)))
        elif len(result) > self.embedding_size:
            result = result[: self.embedding_size]
        return [round(float(v), 6) for v in result]

    def _build_evidence(
        self,
        frame: np.ndarray,
        face_obj,
        x: int,
        y: int,
        width: int,
        height: int,
        frame_width: int,
        frame_height: int,
        score: float,
    ) -> FaceQualityEvidence:
        """构建单张人脸的结构化质检证据。

        分层执行：
        1. 图像质量计算（清晰度/亮度/对比度）；
        2. 关键点完整性与几何合理性；
        3. 姿态与遮挡估计；
        4. 二次人脸验证（face_validity_score）独立于第一阶段检测置信度。

        技术问题（图像读取/裁剪异常）不应伪装成 non_face——本函数始终返回证据，
        face_validity_score 在无法验证时取检测置信度并标注 pose_estimable=False，
        由后端策略引擎决定是否标记可重试失败。
        """
        reasons: list[str] = []

        # --- 图像质量 ---
        crop_bgr = frame[y : y + height, x : x + width]
        if crop_bgr.size == 0:
            # 裁剪异常：返回最小证据，face_validity 用检测分兜底，不杜撰原因码。
            return FaceQualityEvidence(
                face_validity_score=round(min(score, 1.0), 6),
                pixel_width=width,
                pixel_height=height,
                sharpness=0.0,
                brightness=0.0,
                contrast=0.0,
                landmark_completeness=0.0,
                landmark_geometry_score=0.0,
                pose_estimable=False,
                quality_reasons=reasons,
            )

        crop_gray = cv2.cvtColor(crop_bgr, cv2.COLOR_BGR2GRAY)
        sharpness = float(cv2.Laplacian(crop_gray, cv2.CV_64F).var())
        brightness = float(crop_gray.mean())
        contrast = float(crop_gray.std())

        if width < _MIN_FACE_PIXELS or height < _MIN_FACE_PIXELS:
            reasons.append(QUALITY_REASON_TOO_SMALL)
        if sharpness < _SHARPNESS_BLUR_THRESHOLD:
            reasons.append(QUALITY_REASON_BLURRED)
        if brightness >= _BRIGHTNESS_OVEREXPOSED:
            reasons.append(QUALITY_REASON_OVEREXPOSED)
        if brightness <= _BRIGHTNESS_UNDEREXPOSED:
            reasons.append(QUALITY_REASON_UNDEREXPOSED)

        # --- 关键点完整性与几何 ---
        landmark_completeness = 1.0
        landmark_geometry_score = 1.0
        kps = getattr(face_obj, "kps", None)
        # kps 可能是 None、空数组、或非 ndarray；统一转 ndarray 后判空。
        kps_arr = np.asarray(kps) if kps is not None else np.empty((0,))
        if kps_arr.size == 0 or kps_arr.ndim < 2 or kps_arr.shape[0] < 5:
            landmark_completeness = 0.0
            landmark_geometry_score = 0.0
            reasons.append(QUALITY_REASON_INVALID_LANDMARKS)
        else:
            # 5 个关键点：左眼、右眼、鼻、左嘴角、右嘴角。
            kps_arr = kps_arr.astype(np.float64)
            # 完整性：坐标有限的比例。
            finite = np.isfinite(kps_arr[:, 0]) & np.isfinite(kps_arr[:, 1])
            landmark_completeness = round(float(finite.sum()) / 5.0, 6)
            if landmark_completeness < _LANDMARK_COMPLETENESS_LOW:
                reasons.append(QUALITY_REASON_INVALID_LANDMARKS)

            # 几何合理性：左右眼间距应大于 0，且双眼大致水平（俯仰角合理）。
            if finite[:2].all():
                left_eye = kps_arr[0]
                right_eye = kps_arr[1]
                eye_dx = right_eye[0] - left_eye[0]
                eye_dy = right_eye[1] - left_eye[1]
                eye_dist = float(np.hypot(eye_dx, eye_dy))
                if eye_dist <= 0:
                    landmark_geometry_score = 0.0
                    reasons.append(QUALITY_REASON_BAD_GEOMETRY)
                else:
                    # 双眼连线与水平线夹角（度）。过大说明姿态极端或检测错位。
                    roll = float(np.degrees(np.arctan2(eye_dy, eye_dx)))
                    if abs(roll) > 45:
                        landmark_geometry_score = min(landmark_geometry_score, 0.3)
                        reasons.append(QUALITY_REASON_BAD_GEOMETRY)
                    # 几何分随眼距/框宽比衰减：眼距应占框宽相当比例。
                    ratio = eye_dist / float(width) if width > 0 else 0.0
                    landmark_geometry_score = round(min(1.0, max(0.0, ratio / 0.4)), 6)
                    if landmark_geometry_score < _LANDMARK_GEOMETRY_LOW:
                        reasons.append(QUALITY_REASON_BAD_GEOMETRY)
            else:
                landmark_geometry_score = 0.0
                reasons.append(QUALITY_REASON_BAD_GEOMETRY)

        # --- 姿态与遮挡 ---
        pose = getattr(face_obj, "pose", None)
        yaw: float | None = None
        pitch: float | None = None
        roll: float | None = None
        pose_estimable = True
        occluded = False
        if isinstance(pose, np.ndarray) and pose.size >= 3:
            yaw = round(float(pose[0]), 6)
            pitch = round(float(pose[1]), 6)
            roll = round(float(pose[2]), 6)
            if abs(yaw) > _EXTREME_POSE_YAW:
                reasons.append(QUALITY_REASON_EXTREME_POSE)
        else:
            # insightface buffalo_sc 默认不输出 pose，标注不可估计，避免误判。
            pose_estimable = False

        # 遮挡启发：关键点完整但几何分极低时视为疑似遮挡。
        if landmark_geometry_score < 0.2 and landmark_completeness >= _LANDMARK_COMPLETENESS_LOW:
            occluded = True
            reasons.append(QUALITY_REASON_OCCLUDED)

        # --- 二次人脸验证 ---
        # det_score 是第一阶段检测置信度；embedding 非零长度说明 ArcFace 成功编码，
        # 是“真实人脸”的二次佐证。结合关键点几何分给出 face_validity_score。
        embedding_valid = bool(
            getattr(face_obj, "normed_embedding", None) is not None
            and np.asarray(face_obj.normed_embedding).size > 0
        )
        if not embedding_valid:
            reasons.append(QUALITY_REASON_POSE_UNCERTAIN)

        geometry_component = (landmark_completeness + landmark_geometry_score) / 2.0
        # embedding 非空给 +0.2 佐证，几何分占 0.5，检测分占 0.3。
        validity = 0.3 * min(max(score, 0.0), 1.0) + 0.5 * geometry_component
        if embedding_valid:
            validity += 0.2
        face_validity_score = round(min(max(validity, 0.0), 1.0), 6)
        # 高确定性非人脸与灰区的最终判定由后端策略引擎完成；此处只产出分值与原因码，
        # 不在证据层杜撰 non_face 标签（避免技术问题被误判为非人脸）。

        return FaceQualityEvidence(
            face_validity_score=face_validity_score,
            pixel_width=int(width),
            pixel_height=int(height),
            sharpness=round(sharpness, 6),
            brightness=round(brightness, 6),
            contrast=round(contrast, 6),
            landmark_completeness=round(landmark_completeness, 6),
            landmark_geometry_score=round(landmark_geometry_score, 6),
            yaw=yaw,
            pitch=pitch,
            roll=roll,
            pose_estimable=pose_estimable,
            occluded=occluded,
            quality_reasons=list(dict.fromkeys(reasons)),  # 去重保序
            rule_version=FACE_QUALITY_RULE_VERSION,
            model_version=FACE_QUALITY_MODEL_VERSION,
        )

    def _estimate_quality(
        self,
        frame: np.ndarray,
        x: int,
        y: int,
        width: int,
        height: int,
        frame_width: int,
        frame_height: int,
        score: float,
    ) -> float:
        crop = cv2.cvtColor(frame[y : y + height, x : x + width], cv2.COLOR_BGR2GRAY)
        if crop.size == 0:
            return round(score * 0.45, 6)
        area_ratio = (width * height) / float(frame_width * frame_height)
        sharpness = cv2.Laplacian(crop, cv2.CV_64F).var()
        normalized_area = min(max(area_ratio / 0.12, 0.0), 1.0)
        normalized_sharpness = min(max(sharpness / 600.0, 0.0), 1.0)
        normalized_score = min(max(score, 0.0), 1.0)
        return round((normalized_score * 0.45) + (normalized_area * 0.2) + (normalized_sharpness * 0.35), 6)

    def _init_insightface(self):
        from insightface.app import FaceAnalysis

        providers = self._get_providers()
        root = os.environ.get("INSIGHTFACE_HOME", self.settings.model_cache_dir)
        os.makedirs(root, exist_ok=True)

        # 打印使用的 provider（方便调试）
        import logging
        logger = logging.getLogger(__name__)
        logger.info(f"InsightFace using providers: {providers}")

        app = FaceAnalysis(
            name=self.settings.model_pack,
            root=root,
            providers=providers,
        )
        det_size = self.settings.det_size
        app.prepare(ctx_id=0, det_size=(det_size, det_size))
        return app

    def _get_providers(self) -> list[str]:
        import platform

        device = self.settings.onnx_device.lower()

        # macOS Apple Silicon - 优先使用 CoreML
        if platform.system() == "Darwin" and platform.machine() == "arm64":
            # 检查 CoreML 是否可用
            try:
                import onnxruntime as ort
                available_providers = ort.get_available_providers()
                if "CoreMLExecutionProvider" in available_providers:
                    return ["CoreMLExecutionProvider", "CPUExecutionProvider"]
            except Exception:
                pass
            return ["CPUExecutionProvider"]

        if device == "cuda":
            return ["CUDAExecutionProvider", "CPUExecutionProvider"]
        return ["CPUExecutionProvider"]


def _bbox_iou(ax: float, ay: float, aw: float, ah: float, bx: float, by: float, bw: float, bh: float) -> float:
    """两个归一化 bbox 的 IoU。与后端 service.bboxIoU 算法一致。"""
    if aw <= 0 or ah <= 0 or bw <= 0 or bh <= 0:
        return 0.0
    inter_x1 = max(ax, bx)
    inter_y1 = max(ay, by)
    inter_x2 = min(ax + aw, bx + bw)
    inter_y2 = min(ay + ah, by + bh)
    if inter_x2 <= inter_x1 or inter_y2 <= inter_y1:
        return 0.0
    inter_area = (inter_x2 - inter_x1) * (inter_y2 - inter_y1)
    union_area = aw * ah + bw * bh - inter_area
    if union_area <= 0:
        return 0.0
    return inter_area / union_area
