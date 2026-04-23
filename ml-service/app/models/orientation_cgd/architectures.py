"""Architecture configurations for inference (input sizes only)."""

ARCHITECTURES = {
    "vit_tiny_patch16_224.augreg_in21k_ft_in1k": {"input_size": 224},
    "vit_base_patch16_224.augreg_in21k_ft_in1k": {"input_size": 224},
    "efficientvit_b0.r224_in1k": {"input_size": 224},
    "efficientvit_b3.r224_in1k": {"input_size": 224},
    "convnextv2_atto.fcmae_ft_in1k": {"input_size": 224},
    "convnextv2_base.fcmae_ft_in22k_in1k": {"input_size": 224},
    "efficientnetv2_rw_t.ra2_in1k": {"input_size": 224},
    "efficientnetv2_rw_m.agc_in1k": {"input_size": 320},
    "mambaout_tiny.in1k": {"input_size": 224},
    "mambaout_base.in1k": {"input_size": 224},
    "focalnet_tiny_lrf.ms_in1k": {"input_size": 224},
    "focalnet_base_lrf.ms_in1k": {"input_size": 224},
    "edgenext_xx_small.in1k": {"input_size": 256},
    "edgenext_base.in21k_ft_in1k": {"input_size": 256},
    "swin_tiny_patch4_window7_224": {"input_size": 224},
    "swin_base_patch4_window7_224": {"input_size": 224},
}


def get_default_input_size(architecture_name):
    """Get default input size for an architecture."""
    return ARCHITECTURES.get(architecture_name, {}).get("input_size", 224)
