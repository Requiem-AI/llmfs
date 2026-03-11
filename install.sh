#!/usr/bin/env bash
set -euo pipefail

REPO="Requiem-AI/llmfs"
ASSET="llmfs-linux-amd64.tar.gz"
API_URL="https://api.github.com/repos/${REPO}/releases/latest"

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

need_cmd curl
need_cmd tar

os="$(uname -s)"
arch="$(uname -m)"
if [[ "$os" != "Linux" ]]; then
  echo "unsupported OS: ${os} (installer currently supports Linux only)" >&2
  exit 1
fi
if [[ "$arch" != "x86_64" ]]; then
  echo "unsupported architecture: ${arch} (installer currently supports x86_64 only)" >&2
  exit 1
fi

tag="$(curl -fsSL "$API_URL" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
if [[ -z "$tag" ]]; then
  echo "failed to resolve latest release tag from GitHub API" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

archive="${tmpdir}/${ASSET}"
url="https://github.com/${REPO}/releases/download/${tag}/${ASSET}"
echo "downloading ${url}"
curl -fL "$url" -o "$archive"

tar -xzf "$archive" -C "$tmpdir"
bin_path="${tmpdir}/llmfs-linux-amd64/llmfs"
if [[ ! -f "$bin_path" ]]; then
  echo "archive did not contain expected binary at llmfs-linux-amd64/llmfs" >&2
  exit 1
fi

install_dir="${INSTALL_DIR:-}"
if [[ -z "$install_dir" ]]; then
  if [[ -w "/usr/local/bin" ]]; then
    install_dir="/usr/local/bin"
  else
    install_dir="${HOME}/.local/bin"
  fi
fi

mkdir -p "$install_dir"
install -m 0755 "$bin_path" "${install_dir}/llmfs"

echo "installed llmfs ${tag} to ${install_dir}/llmfs"
if ! command -v llmfs >/dev/null 2>&1; then
  echo "note: add ${install_dir} to your PATH to run 'llmfs' directly"
fi
