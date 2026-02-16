#!/bin/bash
set -euo pipefail

# Fetch latest proxies from proxifly/free-proxy-list
# https://github.com/proxifly/free-proxy-list

SRC_URL="https://cdn.jsdelivr.net/gh/proxifly/free-proxy-list@main/proxies/all/data.txt"
OUT="configs/proxies.txt"
TMP=$(mktemp -d)

echo "Fetching latest proxy list from proxifly/free-proxy-list..."

curl -fsSL "$SRC_URL" -o "$TMP/raw.txt"

# Normalize, deduplicate, write
{
    echo "# Auto-fetched from proxifly/free-proxy-list at $(date -Iseconds)"
    echo "# Source: https://github.com/proxifly/free-proxy-list"
    echo "# CDN: $SRC_URL"
    echo "# Re-run: bash scripts/update-proxies.sh"
    echo "#"
    grep -v '^#\|^$' "$TMP/raw.txt" | sort -u
} > "$OUT"

rm -rf "$TMP"

COUNT=$(grep -cv '^#\|^$' "$OUT" || true)
echo "Done. Wrote $COUNT proxies to $OUT"
echo "  HTTP:   $(grep -c '^http://'   "$OUT" || echo 0)"
echo "  HTTPS:  $(grep -c '^https://'  "$OUT" || echo 0)"
echo "  SOCKS4: $(grep -c '^socks4://' "$OUT" || echo 0)"
echo "  SOCKS5: $(grep -c '^socks5://' "$OUT" || echo 0)"
