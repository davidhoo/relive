"""Export CGD orientation model to ONNX format.

Usage:
    python export_to_onnx.py --output orientation_model.onnx

This script downloads the pretrained model and exports it to ONNX format.
The exported model takes a 224x224 RGB image tensor and outputs a 360-bin
probability distribution over rotation angles.
"""

import argparse
import torch
import torch.nn as nn


class ONNXExportableModel(nn.Module):
    """Wrapper model for ONNX export.

    The CGDAngleEstimation model uses PyTorch Lightning which adds complexity.
    This wrapper extracts just the core inference logic for clean ONNX export.
    """

    def __init__(self, cgd_model):
        super().__init__()
        self.backbone = cgd_model.model  # timm model
        self.softmax = nn.Softmax(dim=1)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        """Forward pass returning probability distribution over 360 angles.

        Args:
            x: Input image tensor [B, 3, 224, 224]

        Returns:
            Probability distribution [B, 360] over rotation angles
        """
        logits = self.backbone(x)
        return self.softmax(logits)


def main():
    parser = argparse.ArgumentParser(description="Export CGD model to ONNX")
    parser.add_argument("--output", "-o", default="orientation_model.onnx",
                        help="Output ONNX file path")
    parser.add_argument("--model", default="cgd_mambaout_base_coco2017.ckpt",
                        help="Model checkpoint name")
    parser.add_argument("--opset", type=int, default=14, help="ONNX opset version")
    parser.add_argument("--simplify", action="store_true", help="Simplify ONNX model")
    args = parser.parse_args()

    print("Loading CGD model from HuggingFace...")
    from app.models.orientation_cgd import CGDAngleEstimation

    # Map short name to full model name
    model_name_map = {
        "coco2017": "CGD + MambaOut Base (COCO 2017) — 2.84° MAE",
        "coco2014": "CGD + MambaOut Base (COCO 2014) — 3.71° MAE",
        "cgd_mambaout_base_coco2017.ckpt": "CGD + MambaOut Base (COCO 2017) — 2.84° MAE",
        "cgd_mambaout_base_coco2014.ckpt": "CGD + MambaOut Base (COCO 2014) — 3.71° MAE",
    }

    model_display_name = model_name_map.get(args.model, args.model)
    cgd_model = CGDAngleEstimation.from_pretrained(
        "maxwoe/image-rotation-angle-estimation",
        model_name=model_display_name
    )
    cgd_model.eval()

    print("Creating ONNX-exportable wrapper...")
    model = ONNXExportableModel(cgd_model)
    model.eval()

    # Create dummy input
    dummy_input = torch.randn(1, 3, 224, 224)

    print(f"Exporting to ONNX (opset {args.opset})...")
    # Use legacy export to avoid external data files
    torch.onnx.export(
        model,
        dummy_input,
        args.output,
        opset_version=args.opset,
        input_names=["image"],
        output_names=["angle_distribution"],
        dynamic_axes={
            "image": {0: "batch_size"},
            "angle_distribution": {0: "batch_size"},
        },
        do_constant_folding=True,
        export_params=True,
        dynamo=False,
    )

    print(f"Model exported to: {args.output}")

    # Verify export
    import onnx
    onnx_model = onnx.load(args.output)
    onnx.checker.check_model(onnx_model)
    print("ONNX model validation passed!")

    # Print model info
    import os
    file_size_mb = os.path.getsize(args.output) / (1024 * 1024)
    print(f"\nModel info:")
    print(f"  Input: image [B, 3, 224, 224]")
    print(f"  Output: angle_distribution [B, 360]")
    print(f"  File size: {file_size_mb:.1f} MB")

    # Optionally simplify
    if args.simplify:
        try:
            import onnxsim
            print("\nSimplifying ONNX model...")
            onnx_model, check = onnxsim.simplify(onnx_model)
            onnx.save(onnx_model, args.output)
            print("Simplification complete!")
        except ImportError:
            print("onnxsim not installed, skipping simplification")

    # Test with onnxruntime
    try:
        import onnxruntime as ort
        import numpy as np

        print("\nTesting with ONNX Runtime...")
        session = ort.InferenceSession(args.output)
        input_name = session.get_inputs()[0].name
        output_name = session.get_outputs()[0].name

        # Test inference
        test_input = np.random.randn(1, 3, 224, 224).astype(np.float32)
        output = session.run([output_name], {input_name: test_input})[0]

        print(f"  Input shape: {test_input.shape}")
        print(f"  Output shape: {output.shape}")
        print(f"  Output sum: {output.sum():.4f} (should be ~1.0)")
        print(f"  Predicted angle: {output.argmax()}°")

    except ImportError:
        print("onnxruntime not installed, skipping inference test")

    print("\n✅ Export complete!")


if __name__ == "__main__":
    main()
