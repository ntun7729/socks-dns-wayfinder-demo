#!/usr/bin/env python3
import argparse
import json

parser = argparse.ArgumentParser()
parser.add_argument("--direct", required=True)
parser.add_argument("--tunnel", required=True)
args = parser.parse_args()

with open(args.direct, encoding="utf-8") as handle:
    direct = json.load(handle)
with open(args.tunnel, encoding="utf-8") as handle:
    tunnel = json.load(handle)

baseline = direct["median_mbps"]
through = tunnel["median_mbps"]
overhead = (1.0 - through / baseline) * 100 if baseline else 0.0
result = {
    "payload_mib": round(direct["bytes"] / (1024 * 1024), 2),
    "runs": direct["runs"],
    "direct_loopback": direct,
    "socks5_tls": tunnel,
    "median_throughput_loss_percent": overhead,
}
print(json.dumps(result, indent=2, sort_keys=True))
print(
    "speed-summary: direct median %.2f Mbps, SOCKS5/TLS median %.2f Mbps, median loss %.2f%%"
    % (baseline, through, overhead)
)
