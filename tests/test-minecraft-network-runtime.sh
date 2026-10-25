#!/usr/bin/bash
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
python3 -B "${repo_root}/tests/test-minecraft-network-runtime.py"
