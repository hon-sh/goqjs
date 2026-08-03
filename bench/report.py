#!/usr/bin/env python3
"""Build markdown comparison tables from oha JSON metrics or a legacy .txt log."""

from __future__ import annotations

import argparse
import json
import re
from pathlib import Path


# Known order; unknown scenario names from metrics still appear after these.
SCENARIO_ORDER = ("latency", "concurrency", "cpu-parallel", "render-heavy")

SCENARIO_TITLES = {
    "latency": "Latency (low concurrency)",
    "concurrency": "Concurrency / throughput",
    "cpu-parallel": "CPU-parallel (heavy fib)",
    "render-heavy": "Render-heavy (long list, little/no sleep)",
}


def ms(x: float | None) -> str:
    if x is None:
        return "—"
    if x >= 100:
        return f"{x:.1f}"
    if x >= 10:
        return f"{x:.2f}"
    return f"{x:.3f}"


def rps(x: float | None) -> str:
    if x is None:
        return "—"
    if x >= 1000:
        return f"{x:,.0f}"
    return f"{x:.1f}"


def pct(x: float | None) -> str:
    if x is None:
        return "—"
    return f"{x * 100:.2f}%"


def load_oha_json(path: Path) -> dict:
    data = json.loads(path.read_text())
    summary = data.get("summary") or {}
    metrics = data.get("metrics") or {}
    lat = metrics.get("latency_ms") or {}
    # summary times are seconds; prefer metrics.latency_ms
    avg_ms = lat.get("mean")
    if avg_ms is None and "average" in summary:
        avg_ms = summary["average"] * 1000
    p50 = lat.get("p50")
    p95 = lat.get("p95")
    p99 = lat.get("p99")
    mx = lat.get("max")
    if p50 is None:
        lp = data.get("latencyPercentiles") or {}
        p50 = (lp.get("p50") or 0) * 1000 if lp.get("p50") is not None else None
        p95 = (lp.get("p95") or 0) * 1000 if lp.get("p95") is not None else None
        p99 = (lp.get("p99") or 0) * 1000 if lp.get("p99") is not None else None
    if mx is None and "slowest" in summary:
        mx = summary["slowest"] * 1000
    return {
        "success": summary.get("successRate", metrics.get("success_rate")),
        "rps": summary.get("requestsPerSec", metrics.get("requests_per_sec")),
        "avg_ms": avg_ms,
        "p50_ms": p50,
        "p95_ms": p95,
        "p99_ms": p99,
        "max_ms": mx,
    }


def parse_txt(path: Path) -> tuple[dict[str, str], dict[tuple[str, str], dict]]:
    """Parse legacy tee'd oha text log → (meta, {(runtime, scenario): metrics})."""
    text = path.read_text(errors="replace")
    meta: dict[str, str] = {}
    for line in text.splitlines()[:30]:
        line = line.strip()
        if line.startswith("host="):
            for part in line.split():
                if "=" in part:
                    k, v = part.split("=", 1)
                    meta[k] = v
        elif line.startswith(("oha=", "bun=", "deno=", "deno.version=")):
            k, v = line.split("=", 1)
            meta[k] = v.strip()

    rows: dict[tuple[str, str], dict] = {}
    blocks = re.split(r"\n----- ", text)
    for block in blocks[1:]:
        header, _, body = block.partition(" -----")
        m = re.match(r"(\S+)\s*/\s*([a-z0-9-]+)\b", header.strip())
        if not m:
            continue
        runtime, scenario = m.group(1), m.group(2)

        def grab_ms(label: str) -> float | None:
            # oha may print "123.4 ms" or "1.23 s" / "1.23 sec"
            mm = re.search(
                rf"{label}:\s*([0-9.]+)\s*(ms|secs?|s)\b",
                body,
            )
            if not mm:
                return None
            val = float(mm.group(1))
            unit = mm.group(2)
            return val if unit == "ms" else val * 1000.0

        def grab(pat: str) -> float | None:
            mm = re.search(pat, body)
            return float(mm.group(1)) if mm else None

        def grab_pct(pct_label: str) -> float | None:
            mm = re.search(
                rf"{re.escape(pct_label)} in ([0-9.]+)\s*(ms|secs?|s)\b",
                body,
            )
            if not mm:
                return None
            val = float(mm.group(1))
            return val if mm.group(2) == "ms" else val * 1000.0

        success = grab(r"Success rate:\s*([0-9.]+)%")
        avg = grab_ms("Average")
        slow = grab_ms("Slowest")
        rps_v = grab(r"Requests/sec:\s*([0-9.]+)")
        p50 = grab_pct("50.00%")
        p95 = grab_pct("95.00%")
        p99 = grab_pct("99.00%")
        rows[(runtime, scenario)] = {
            "success": (success / 100.0) if success is not None else None,
            "rps": rps_v,
            "avg_ms": avg,
            "p50_ms": p50,
            "p95_ms": p95,
            "p99_ms": p99,
            "max_ms": slow,
        }
    return meta, rows


def collect_json_dir(metrics_dir: Path) -> dict[tuple[str, str], dict]:
    rows: dict[tuple[str, str], dict] = {}
    for p in sorted(metrics_dir.glob("*.json")):
        # name: {runtime}.{scenario}.json
        stem = p.stem
        if "." not in stem:
            continue
        runtime, scenario = stem.split(".", 1)
        rows[(runtime, scenario)] = load_oha_json(p)
    return rows


def ordered_scenarios(rows: dict[tuple[str, str], dict]) -> list[str]:
    present = {s for _, s in rows}
    ordered = [s for s in SCENARIO_ORDER if s in present]
    for s in sorted(present):
        if s not in ordered:
            ordered.append(s)
    return ordered


def render_md(
    meta: dict[str, str],
    rows: dict[tuple[str, str], dict],
    title: str,
) -> str:
    runtimes = sorted({r for r, _ in rows}, key=lambda x: (x != "bun", x != "deno", x))
    lines: list[str] = []
    lines.append(f"# {title}")
    lines.append("")
    if meta:
        lines.append("## Environment")
        lines.append("")
        lines.append("| key | value |")
        lines.append("|-----|-------|")
        for k in sorted(meta):
            lines.append(f"| `{k}` | {meta[k]} |")
        lines.append("")

    for scenario in ordered_scenarios(rows):
        title_s = SCENARIO_TITLES.get(scenario, scenario)
        lines.append(f"## {title_s}")
        lines.append("")
        lines.append(
            "| runtime | success | RPS | avg ms | p50 ms | p95 ms | p99 ms | max ms |"
        )
        lines.append(
            "|---------|---------|-----|--------|--------|--------|--------|--------|"
        )
        for rt in runtimes:
            m = rows.get((rt, scenario))
            if not m:
                lines.append(f"| `{rt}` | — | — | — | — | — | — | — |")
                continue
            lines.append(
                "| `{rt}` | {success} | {rps} | {avg} | {p50} | {p95} | {p99} | {mx} |".format(
                    rt=rt,
                    success=pct(m.get("success")),
                    rps=rps(m.get("rps")),
                    avg=ms(m.get("avg_ms")),
                    p50=ms(m.get("p50_ms")),
                    p95=ms(m.get("p95_ms")),
                    p99=ms(m.get("p99_ms")),
                    mx=ms(m.get("max_ms")),
                )
            )
        lines.append("")

    lines.append("_Times in milliseconds. Generated by `bench/report.py`._")
    lines.append("")
    return "\n".join(lines)


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--metrics-dir", type=Path, help="directory of runtime.scenario.json")
    ap.add_argument("--from-txt", type=Path, help="legacy tee'd oha text log")
    ap.add_argument("--out", type=Path, required=True, help="output .md path")
    ap.add_argument("--title", default="Fib HTTP bench")
    ap.add_argument("--meta", action="append", default=[], help="key=value (repeatable)")
    args = ap.parse_args()

    meta: dict[str, str] = {}
    for item in args.meta:
        if "=" in item:
            k, v = item.split("=", 1)
            meta[k] = v

    if args.metrics_dir:
        rows = collect_json_dir(args.metrics_dir)
    elif args.from_txt:
        file_meta, rows = parse_txt(args.from_txt)
        meta = {**file_meta, **meta}
    else:
        ap.error("need --metrics-dir or --from-txt")

    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(render_md(meta, rows, args.title))
    print(f"wrote {args.out}")


if __name__ == "__main__":
    main()
