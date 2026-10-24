#!/usr/bin/bash
# Offline official Minecraft Services response fixtures. Do not invoke upstream.
set -euo pipefail
repo_root="$(cd -- "$(dirname -- "$0")/.." && pwd)"
source "${repo_root}/mjust/libexec/common.sh"
fixture='{"result":{"links":[{"downloadType":"serverBedrockPreviewLinux","downloadUrl":"https://www.minecraft.net/bedrockdedicatedserver/bin-linux-preview/bedrock-server-1.27.0.1.zip"},{"downloadType":"serverBedrockWindows","downloadUrl":"https://www.minecraft.net/bedrockdedicatedserver/bin-win/bedrock-server-1.26.50.1.zip"},{"downloadType":"serverBedrockLinux","downloadUrl":"https://www.minecraft.net/bedrockdedicatedserver/bin-linux/bedrock-server-1.26.50.1.zip"}]}}'
curl() {
    [[ ${*: -1} == https://net.web.minecraft-services.net/api/v1.0/download/links ]] || return 1
    [[ ${offline:-no} != yes ]] || return 22
    printf '%s' "${fixture}"
}
[[ $(resolve_bedrock_release) == 1.26.50.1 ]]
reject() { if resolve_bedrock_release; then echo 'Unverified Bedrock release accepted' >&2; exit 1; fi; }
fixture='{"result":{"links":[{"downloadType":"serverBedrockPreviewLinux","downloadUrl":"https://www.minecraft.net/bedrockdedicatedserver/bin-linux/bedrock-server-1.27.0.1.zip"}]}}'
reject
for url in \
    https://www.minecraft.net/bedrockdedicatedserver/bin-linux-preview/bedrock-server-1.27.0.1.zip \
    https://www.minecraft.net/bedrockdedicatedserver/bin-linux/bedrock-server-LATEST.zip \
    https://www.minecraft.net/bedrockdedicatedserver/bin-linux/bedrock-server-1.27.0-preview.zip \
    https://example.org/bedrock-server-1.26.50.1.zip; do
    fixture="$(jq -cn --arg url "${url}" '{result:{links:[{downloadType:"serverBedrockLinux",downloadUrl:$url}]}}')"
    reject
done
fixture='{"result":{"links":[]}}'
reject
offline=yes
reject
