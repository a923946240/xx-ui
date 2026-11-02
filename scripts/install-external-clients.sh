#!/usr/bin/env bash
set -euo pipefail

destination=${1:?Usage: install-external-clients.sh DESTINATION [naive|mieru|trusttunnel|all]}
selected=${2:-all}
case "$selected" in naive|mieru|trusttunnel|all) ;; *) echo 'Unknown client' >&2; exit 1;; esac
case "$(uname -sm)" in
  'Linux x86_64') arch=amd64 ;;
  'Linux aarch64') arch=arm64 ;;
  *) echo 'Only Linux amd64 and arm64 are supported' >&2; exit 1 ;;
esac
for tool in curl tar sha256sum xz; do command -v "$tool" >/dev/null || { echo "Missing tool: $tool" >&2; exit 1; }; done
mkdir -p "$destination"
destination=$(cd "$destination" && pwd -P)
temporary=$(mktemp -d)
trap 'rm -rf -- "$temporary"' EXIT

for client in naive mieru trusttunnel; do
  if [[ $selected != all && $selected != "$client" ]]; then continue; fi
  case "$client/$arch" in
    naive/amd64)
      url=https://github.com/klzgrad/naiveproxy/releases/download/v154.0.8037.49-5/naiveproxy-v154.0.8037.49-5-linux-x64.tar.xz
      sha=80214dfea1767f6ecf63a3a79dc9f48b203460f51abc609357fff48dd5844c7d ;;
    naive/arm64)
      url=https://github.com/klzgrad/naiveproxy/releases/download/v154.0.8037.49-5/naiveproxy-v154.0.8037.49-5-linux-arm64.tar.xz
      sha=8f4d917fa39a5d8cf1c17fc12ffd99ba84c015b62b43461d09cc074ca4114d8d ;;
    mieru/amd64)
      url=https://github.com/enfein/mieru/releases/download/v3.39.0/mieru_3.39.0_linux_amd64.tar.gz
      sha=81f549ffd7be2df6958b1051aad10e58884889d92e691c1e43822c42091d1dfe ;;
    mieru/arm64)
      url=https://github.com/enfein/mieru/releases/download/v3.39.0/mieru_3.39.0_linux_arm64.tar.gz
      sha=23337061e8cbba9869ef17ce2c44121a318a66c40da5e2655e8c0e404b0f18ed ;;
    trusttunnel/amd64)
      url=https://github.com/TrustTunnel/TrustTunnelClient/releases/download/v1.1.11/trusttunnel_client-v1.1.11-linux-x86_64.tar.gz
      sha=00e0546a6b35840dbb92b741dff3da6732a74bf2d6c581e044bb4567c0d96825 ;;
    trusttunnel/arm64)
      url=https://github.com/TrustTunnel/TrustTunnelClient/releases/download/v1.1.11/trusttunnel_client-v1.1.11-linux-aarch64.tar.gz
      sha=880e9c8afd5cce7ff38c53978f0ea530ff95e679bcfef69c5127b26b3692d110 ;;
  esac
  archive="$temporary/$client.archive"
  curl --proto '=https' --tlsv1.2 -fL --retry 2 --connect-timeout 15 --max-time 300 "$url" -o "$archive"
  printf '%s  %s\n' "$sha" "$archive" | sha256sum -c -
  entries=$(tar -tf "$archive")
  binary=$client
  if [[ $client == trusttunnel ]]; then binary=trusttunnel_client; fi
  matches=()
  while IFS= read -r entry; do
    if [[ ${entry##*/} == "$binary" ]]; then matches+=("$entry"); fi
  done <<< "$entries"
  if [[ ${#matches[@]} != 1 ]]; then echo "Expected one $binary executable in verified archive" >&2; exit 1; fi
  tar -xOf "$archive" "${matches[0]}" > "$temporary/$client"
  chmod 755 "$temporary/$client"
  install -m 755 "$temporary/$client" "$destination/.$client.new"
  mv -f -- "$destination/.$client.new" "$destination/$client"
  printf 'Installed %s (%s)\n' "$client" "$arch"
done
