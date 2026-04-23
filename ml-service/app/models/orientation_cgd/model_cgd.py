"""
Circular Gaussian Distribution (CGD) for Image Orientation Estimation (Inference Only)

Represents angles as probability distributions over discretized angle bins.
Model output: Probability distribution over 360 angle bins (1 degree resolution)
"""

import math
from typing import Dict, Any

import torch
import torch.nn as nn
import torch.nn.functional as F
import torchvision.transforms as transforms
import pytorch_lightning as pl
import timm
import timm.data
from PIL import Image
import numpy as np
from loguru import logger


class CircularGaussianDistribution(nn.Module):
    """Circular Gaussian Distribution module for 360 degree image orientation."""

    def __init__(self, num_bins: int = 360, sigma: float = 6.0):
        super().__init__()
        self.num_bins = num_bins
        self.sigma = sigma
        self.bin_size = 360.0 / num_bins

        bin_centers = torch.arange(0, 360, self.bin_size)
        self.register_buffer('bin_centers', bin_centers)

        logger.info(f"CGD: {num_bins} bins, range [0, 360), sigma={sigma}")

    def distribution_to_angle(self, distributions: torch.Tensor, method: str = 'argmax') -> torch.Tensor:
        """Extract angles from probability distributions.

        Args:
            distributions: Probability distributions [B, num_bins]
            method: 'argmax', 'weighted_average', or 'peak_fitting'

        Returns:
            angles: Extracted angles in degrees [B] in [0, 360)
        """
        if method == 'argmax':
            peak_indices = torch.argmax(distributions, dim=1)
            angles = self.bin_centers[peak_indices]

        elif method == 'weighted_average':
            weights = distributions / (distributions.sum(dim=1, keepdim=True) + 1e-8)
            bin_angles_rad = self.bin_centers * torch.pi / 180.0
            cos_components = torch.cos(bin_angles_rad)
            sin_components = torch.sin(bin_angles_rad)
            avg_cos = torch.sum(weights * cos_components.unsqueeze(0), dim=1)
            avg_sin = torch.sum(weights * sin_components.unsqueeze(0), dim=1)
            angles = torch.atan2(avg_sin, avg_cos) * 180.0 / torch.pi
            angles = angles % 360.0

        elif method == 'peak_fitting':
            peak_indices = torch.argmax(distributions, dim=1)
            angles = torch.zeros_like(peak_indices, dtype=torch.float)
            for i in range(distributions.shape[0]):
                peak_idx = peak_indices[i].item()
                if 0 < peak_idx < self.num_bins - 1:
                    y1 = distributions[i, peak_idx - 1]
                    y2 = distributions[i, peak_idx]
                    y3 = distributions[i, peak_idx + 1]
                    a = 0.5 * (y1 - 2*y2 + y3)
                    b = 0.5 * (y3 - y1)
                    if abs(a) > 1e-8:
                        offset = -b / (2 * a)
                        offset = torch.clamp(offset, -0.5, 0.5)
                    else:
                        offset = 0
                    angles[i] = self.bin_centers[peak_idx] + offset * self.bin_size
                else:
                    angles[i] = self.bin_centers[peak_idx]
        else:
            raise ValueError(f"Unknown extraction method: {method}")

        angles = angles % 360.0
        return angles

    def get_distribution_uncertainty(self, distributions: torch.Tensor) -> torch.Tensor:
        """Calculate entropy-based uncertainty from distribution."""
        log_probs = torch.log(distributions + 1e-8)
        entropy = -torch.sum(distributions * log_probs, dim=1)
        max_entropy = math.log(self.num_bins)
        return entropy / max_entropy


class CGDAngleEstimation(pl.LightningModule):
    """CGD model for 360 degree image orientation estimation (inference only)."""

    def __init__(
        self,
        batch_size: int = 16,
        train_dir: str = "",
        model_name: str = "vit_tiny_patch16_224",
        learning_rate: float = 0.001,
        validation_split: float = 0.1,
        random_seed: int = 42,
        image_size: int = 224,
        num_bins: int = 360,
        sigma: float = 6.0,
        inference_method: str = 'argmax',
        loss_type: str = 'kl_divergence',
        test_dir=None,
        test_rotation_range=360.0,
        test_random_seed=42,
    ) -> None:
        super().__init__()
        self.save_hyperparameters()

        self.model_name = model_name
        self.learning_rate = learning_rate
        self.batch_size = batch_size
        self.train_dir = train_dir
        self.validation_split = validation_split
        self.random_seed = random_seed
        self.image_size = image_size
        self.num_bins = num_bins
        self.sigma = sigma
        self.inference_method = inference_method
        self.loss_type = loss_type

        self.model = timm.create_model(model_name, pretrained=True, num_classes=num_bins)
        self.cgd = CircularGaussianDistribution(num_bins=num_bins, sigma=sigma)

    @classmethod
    def try_load(cls, checkpoint_path=None, **kwargs):
        """Load model from checkpoint."""
        if checkpoint_path:
            logger.info(f"Loading model from checkpoint: {checkpoint_path}")
            model = cls.load_from_checkpoint(checkpoint_path, **kwargs)
            logger.info("Model loaded successfully from checkpoint")
            return model
        raise FileNotFoundError("Checkpoint file not found")

    @classmethod
    def from_pretrained(cls, repo_id, model_name=None):
        """Load a pretrained model from HuggingFace Hub.

        Args:
            repo_id: HuggingFace repo ID (e.g. "maxwoe/image-rotation-angle-estimation")
            model_name: Display name or checkpoint filename from config.json.
                Defaults to the default model.
        """
        import json
        from huggingface_hub import hf_hub_download

        config_path = hf_hub_download(repo_id=repo_id, filename="config.json")
        with open(config_path) as f:
            config = json.load(f)

        if model_name is None:
            model_name = config["default_model"]

        # Look up by display name or by filename
        if model_name in config["models"]:
            model_info = config["models"][model_name]
        else:
            model_info = None
            for info in config["models"].values():
                if info["filename"] == model_name:
                    model_info = info
                    break
            if model_info is None:
                available = [i["filename"] for i in config["models"].values()]
                raise ValueError(f"Unknown model: {model_name}. Available: {available}")

        ckpt_path = hf_hub_download(repo_id=repo_id, filename=model_info["filename"])
        model = cls.try_load(checkpoint_path=ckpt_path, image_size=model_info["input_size"])
        model.eval()
        return model

    def forward(self, x: torch.Tensor, return_logits: bool = False) -> torch.Tensor:
        """Forward pass returning probability distribution over angles."""
        logits = self.model(x)
        if return_logits:
            return logits
        return F.softmax(logits, dim=1)

    def predict_angle(self, image) -> float:
        """Detect the current orientation angle of an image.

        Args:
            image: PIL Image, numpy array, or file path string.
                   For best results, pass PIL Image or numpy array directly.

        Returns:
            Predicted rotation angle in degrees [0, 360).
        """
        self.eval()

        if isinstance(image, str):
            image = Image.open(image).convert('RGB')
        elif isinstance(image, np.ndarray):
            image = Image.fromarray(image).convert('RGB')
        elif not isinstance(image, Image.Image):
            raise TypeError(f"Expected PIL Image, numpy array, or file path, got {type(image)}")
        else:
            image = image.convert('RGB')

        try:
            data_config = timm.data.resolve_model_data_config(self.hparams.model_name)
            data_config['crop_pct'] = 1.0
            data_config['input_size'] = (3, self.image_size, self.image_size)
            transform = timm.data.create_transform(**data_config, is_training=False)
        except Exception:
            transform = transforms.Compose([
                transforms.Resize((self.image_size, self.image_size)),
                transforms.ToTensor(),
                transforms.Normalize(mean=[0.485, 0.456, 0.406], std=[0.229, 0.224, 0.225])
            ])

        image_tensor = transform(image).unsqueeze(0)

        with torch.no_grad():
            pred_distributions = self(image_tensor)
            angle = self.cgd.distribution_to_angle(pred_distributions, method=self.inference_method).item()

        return angle
