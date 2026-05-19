# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Fetch OpenRouter's model catalog and write it into models.csv (replaces existing openrouter/* rows)."""
import argparse
import csv
import json
import re
import sys
import urllib.request
from pathlib import Path

OPENROUTER_MODELS_URL = "https://openrouter.ai/api/v1/models"

CONST = {
    "provider_key": "openrouter",
    "provider_label": "OpenRouter",
    "icon_key": "openrouter",
    "api_format": "openai-completions",
    "base_url": "https://openrouter.ai/api/v1",
}


def money(s):
    if not s:
        return ""
    try:
        v = float(s) * 1_000_000
    except (TypeError, ValueError):
        return ""
    if v == 0:
        return ""
    return format(v, "g")


def to_row(m, header):
    arch = m.get("architecture") or {}
    pricing = m.get("pricing") or {}
    top = m.get("top_provider") or {}
    sp = set(m.get("supported_parameters") or [])
    reasoning = "TRUE" if ("reasoning" in sp or pricing.get("internal_reasoning")) else "FALSE"
    vision = "TRUE" if "image" in (arch.get("input_modalities") or []) else "FALSE"
    desc = (m.get("description") or "").strip().split("\n", 1)[0]
    desc = re.split(r"(?<=[.!?])\s", desc, maxsplit=1)[0][:140]
    row = {
        **CONST,
        "model_id": m["id"],
        "model_name": m.get("name", ""),
        "reasoning": reasoning,
        "vision": vision,
        "context_window": m.get("context_length") or "",
        "max_tokens": top.get("max_completion_tokens") or "",
        "input_cost": money(pricing.get("prompt")),
        "output_cost": money(pricing.get("completion")),
        "cached_read_cost": money(pricing.get("input_cache_read")),
        "cached_write_cost": money(pricing.get("input_cache_write")),
        "tag": "",
        "description": desc,
    }
    return [row.get(c, "") for c in header]


def fetch_models(api_key):
    req = urllib.request.Request(
        OPENROUTER_MODELS_URL,
        headers={"Authorization": f"Bearer {api_key}", "Accept": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read())["data"]


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("api_key", help="OpenRouter API key (sent as Bearer token)")
    ap.add_argument("--csv", default="models.csv", type=Path)
    args = ap.parse_args()

    with args.csv.open(newline="") as f:
        reader = csv.reader(f)
        header = next(reader)
        kept = [r for r in reader if r and r[0] != "openrouter"]

    data = fetch_models(args.api_key)
    new_rows = [to_row(m, header) for m in data]

    with args.csv.open("w", newline="") as f:
        w = csv.writer(f)
        w.writerow(header)
        w.writerows(kept)
        w.writerows(new_rows)
    print(f"wrote {len(kept)} kept + {len(new_rows)} openrouter rows", file=sys.stderr)


if __name__ == "__main__":
    main()
