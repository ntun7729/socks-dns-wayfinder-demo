#!/usr/bin/env python3
import json
import statistics
import sys

if len(sys.argv) != 3:
    raise SystemExit("usage: cloudflare_speed_summary.py DIRECT_JSON CLOUDFLARE_JSON")

with open(sys.argv[1], encoding="utf-8") as handle:
    direct = json.load(handle)
with open(sys.argv[2], encoding="utf-8") as handle:
    cloudflare = json.load(handle)
loss = (1.0 - cloudflare["median_mbps"] / direct["median_mbps"]) * 100.0
result = {
    "direct_median_mbps": direct["median_mbps"],
    "cloudflare_socks5_tls_median_mbps": cloudflare["median_mbps"],
    "cloudflare_relative_loss_percent": loss,
    "direct_samples_mbps": [sample["mbps"] for sample in direct["samples"]],
    "cloudflare_samples_mbps": [sample["mbps"] for sample in cloudflare["samples"]],
    "runs": direct["runs"],
    "payload_bytes": direct["bytes"],
}
print(json.dumps(result, indent=2, sort_keys=True))
print(
    "cloudflare-speed-summary: direct median %.2f Mbps, Cloudflare+s5dns median %.2f Mbps, loss %.2f%%"
    % (direct["median_mbps"], cloudflare["median_mbps"], loss)
)
