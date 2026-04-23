"""Rotation utilities that preserve image content without artificial borders."""

import cv2
import numpy as np
import math


def rotate_image(img, angle, rotation_center=None, expand=False, border_mode=cv2.BORDER_CONSTANT, border_value=0):
    """Rotates an image (angle in degrees) and optionally expands to avoid cropping."""
    h, w = img.shape[:2]
    if rotation_center is None:
        rotation_center = (w/2, h/2)

    M = cv2.getRotationMatrix2D(rotation_center, angle, 1.0)

    if expand:
        abs_cos = abs(M[0, 0])
        abs_sin = abs(M[0, 1])
        wn = int(h * abs_sin + w * abs_cos)
        hn = int(h * abs_cos + w * abs_sin)
        M[0, 2] += wn/2 - rotation_center[0]
        M[1, 2] += hn/2 - rotation_center[1]
    else:
        wn, hn = w, h

    rotated = cv2.warpAffine(
        img, M, (wn, hn), borderMode=border_mode, borderValue=border_value)

    return rotated, M


def largest_rotated_rect(w, h, angle):
    """Compute the largest axis-aligned rectangle within a rotated rectangle."""
    if w <= 0 or h <= 0:
        return 0, 0

    width_is_longer = w >= h
    side_long, side_short = (w, h) if width_is_longer else (h, w)

    sin_a, cos_a = abs(math.sin(angle)), abs(math.cos(angle))
    if side_short <= 2.*sin_a*cos_a*side_long or abs(sin_a-cos_a) < 1e-10:
        x = 0.5*side_short
        wr, hr = (x/sin_a, x/cos_a) if width_is_longer else (x/cos_a, x/sin_a)
    else:
        cos_2a = cos_a*cos_a - sin_a*sin_a
        wr, hr = (w*cos_a - h*sin_a)/cos_2a, (h*cos_a - w*sin_a)/cos_2a

    return wr, hr


def rotate_image_crop_max_area(image, angle):
    """Rotate image and crop to the largest inscribed rectangle (no borders).

    Args:
        image: numpy array (OpenCV image)
        angle: Rotation angle in degrees

    Returns:
        Rotated and cropped numpy array
    """
    h, w = image.shape[:2]
    rotated, _ = rotate_image(image, angle, expand=True)
    wr, hr = largest_rotated_rect(w, h, math.radians(angle))

    h_rot, w_rot = rotated.shape[:2]
    y1 = h_rot//2 - int(hr/2)
    y2 = y1 + int(hr)
    x1 = w_rot//2 - int(wr/2)
    x2 = x1 + int(wr)

    return rotated[y1:y2, x1:x2]
