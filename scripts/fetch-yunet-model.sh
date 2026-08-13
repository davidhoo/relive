#!/usr/bin/env bash
# Fetch and verify the YuNet 2023mar ONNX model used by the v2 independent verifier.
#
# Run BEFORE `docker compose build relive-ml`. The model is NOT committed to Git
# (see .gitignore); it must be present in the build context so the Dockerfile's
# `sha256sum -c` gate can reject a missing or tampered asset at build time.
# Never used at container runtime — the model must not be downloaded after build.
#
# Source, revision, filename and SHA-256 are hard-coded constants. Environment
# variables cannot override the source URL or digest.
set -euo pipefail

COMMIT="47534e27c9851bb1128ccc0102f1145e27f23f98"
MODEL_FILE="face_detection_yunet_2023mar.onnx"
EXPECTED_SHA256="8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4"
URL="https://github.com/opencv/opencv_zoo/raw/${COMMIT}/models/face_detection_yunet/${MODEL_FILE}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEST_DIR="${SCRIPT_DIR}/../ml-service/assets/yunet"
DEST="${DEST_DIR}/${MODEL_FILE}"
PARTIAL="${DEST}.partial"
SUMS="${DEST_DIR}/SHA256SUMS"

mkdir -p "${DEST_DIR}"

sha256_tool() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

verify_sums_file() {
  # Cross-check against the committed manifest so a drifted SHA256SUMS fails here too.
  if [[ ! -f "${SUMS}" ]]; then
    echo "ERROR: ${SUMS} missing — manifest must be committed." >&2
    return 1
  fi
  (cd "${DEST_DIR}" && \
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum -c SHA256SUMS
    else
      shasum -a 256 -c SHA256SUMS
    fi)
}

# Already present: verify in place. Do NOT overwrite a verified-asset location.
if [[ -s "${DEST}" ]]; then
  actual="$(sha256_tool "${DEST}")"
  if [[ "${actual}" == "${EXPECTED_SHA256}" ]]; then
    echo "YuNet model already present and verified: ${DEST}"
    verify_sums_file
    exit 0
  fi
  echo "ERROR: existing ${DEST} has wrong SHA256" >&2
  echo "  expected ${EXPECTED_SHA256}" >&2
  echo "  got      ${actual}" >&2
  echo "Refusing to overwrite. Remove the file manually if the mismatch is intentional." >&2
  exit 1
fi

# Clean up any stale partial from a previous failed run.
rm -f "${PARTIAL}"

echo "Downloading ${MODEL_FILE}"
echo "  from ${URL}"
curl -fsSL -o "${PARTIAL}" "${URL}"

actual="$(sha256_tool "${PARTIAL}")"
if [[ "${actual}" != "${EXPECTED_SHA256}" ]]; then
  rm -f "${PARTIAL}"
  echo "ERROR: SHA256 mismatch for ${MODEL_FILE}" >&2
  echo "  expected ${EXPECTED_SHA256}" >&2
  echo "  got      ${actual}" >&2
  exit 1
fi

# Atomic move into place only after the digest matched.
mv "${PARTIAL}" "${DEST}"
echo "YuNet model fetched and verified: ${DEST}"

verify_sums_file
