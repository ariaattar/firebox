#!/usr/bin/env python3
"""
firebox benchmark runner

Measures end-to-end CLI latency for common workflows and reports:
- mean / p50 / p95 / p99 / min / max
- success/failure counts
- budget pass/fail status (default budget: 200ms)

Examples:
  python3 benchmark.py
  python3 benchmark.py --iterations 25 --warmup 5 --enforce-budget
  python3 benchmark.py --mount-source /Users/me/repo --scenarios run_mount,run_mount_write
  python3 benchmark.py --json-out bench.json --scenarios run_echo,run_mount,sandbox_flow
"""

from __future__ import annotations

import argparse
import json
import math
import os
import subprocess
import sys
import time
from dataclasses import dataclass, asdict
from typing import Dict, Iterable, List, Tuple


@dataclass
class Sample:
    metric: str
    duration_ms: float
    rc: int
    stdout: str
    stderr: str
    command: List[str]


@dataclass
class MetricStats:
    metric: str
    count: int
    ok: int
    fail: int
    mean_ms: float
    p50_ms: float
    p95_ms: float
    p99_ms: float
    min_ms: float
    max_ms: float

    def budget_pass(self, budget_ms: float) -> bool:
        return self.fail == 0 and self.p95_ms <= budget_ms


def run_command(cmd: List[str], timeout_s: float) -> Sample:
    start = time.perf_counter()
    try:
        proc = subprocess.run(
            cmd,
            text=True,
            capture_output=True,
            timeout=timeout_s,
            check=False,
        )
        elapsed = (time.perf_counter() - start) * 1000.0
        return Sample(
            metric="",
            duration_ms=elapsed,
            rc=proc.returncode,
            stdout=proc.stdout or "",
            stderr=proc.stderr or "",
            command=cmd,
        )
    except subprocess.TimeoutExpired as exc:
        elapsed = (time.perf_counter() - start) * 1000.0
        stdout = exc.stdout if isinstance(exc.stdout, str) else ""
        stderr = exc.stderr if isinstance(exc.stderr, str) else ""
        if not stderr:
            stderr = f"timeout after {timeout_s}s"
        return Sample(
            metric="",
            duration_ms=elapsed,
            rc=124,
            stdout=stdout,
            stderr=stderr,
            command=cmd,
        )


def percentile(values: List[float], q: float) -> float:
    if not values:
        return float("nan")
    ordered = sorted(values)
    if len(ordered) == 1:
        return ordered[0]
    pos = (len(ordered) - 1) * q
    lo = int(math.floor(pos))
    hi = int(math.ceil(pos))
    if lo == hi:
        return ordered[lo]
    return ordered[lo] + (ordered[hi] - ordered[lo]) * (pos - lo)


def summarize(metric: str, samples: List[Sample]) -> MetricStats:
    durations = [s.duration_ms for s in samples]
    ok = sum(1 for s in samples if s.rc == 0)
    fail = len(samples) - ok
    return MetricStats(
        metric=metric,
        count=len(samples),
        ok=ok,
        fail=fail,
        mean_ms=sum(durations) / len(durations) if durations else float("nan"),
        p50_ms=percentile(durations, 0.50),
        p95_ms=percentile(durations, 0.95),
        p99_ms=percentile(durations, 0.99),
        min_ms=min(durations) if durations else float("nan"),
        max_ms=max(durations) if durations else float("nan"),
    )


def is_daemon_running(binary: str, timeout_s: float) -> bool:
    sample = run_command([binary, "daemon", "status"], timeout_s)
    return sample.rc == 0


def ensure_daemon_started(binary: str, timeout_s: float) -> Tuple[bool, Sample]:
    already_running = is_daemon_running(binary, timeout_s)
    if already_running:
        sample = run_command([binary, "daemon", "status"], timeout_s)
        sample.metric = "daemon_status"
        return False, sample
    sample = run_command([binary, "daemon", "start"], timeout_s)
    sample.metric = "daemon_start"
    return True, sample


def stop_daemon(binary: str, timeout_s: float) -> Sample:
    sample = run_command([binary, "daemon", "stop"], timeout_s)
    sample.metric = "daemon_stop"
    return sample


def run_simple_scenario(
    metric: str,
    cmd: List[str],
    timeout_s: float,
) -> List[Sample]:
    sample = run_command(cmd, timeout_s)
    sample.metric = metric
    return [sample]


def run_sandbox_flow(
    binary: str,
    mount_source: str,
    mount_dest: str,
    run_script: str,
    iteration: int,
    timeout_s: float,
) -> List[Sample]:
    run_id = f"bench-{int(time.time() * 1000)}-{iteration}"
    volume = f"{mount_source}:{mount_dest}:rw:cow=on"
    samples: List[Sample] = []
    start_total = time.perf_counter()

    create = run_command(
        [
            binary,
            "sandbox",
            "create",
            "--id",
            run_id,
            "--strict-budget=false",
            "-v",
            volume,
            "-w",
            mount_dest,
        ],
        timeout_s,
    )
    create.metric = "sandbox_create"
    samples.append(create)

    started = False
    if create.rc == 0:
        start = run_command([binary, "sandbox", "start", run_id], timeout_s)
        start.metric = "sandbox_start"
        samples.append(start)
        started = start.rc == 0
    else:
        samples.append(
            Sample(
                metric="sandbox_start",
                duration_ms=0.0,
                rc=1,
                stdout="",
                stderr="skipped (create failed)",
                command=[],
            )
        )

    if started:
        exec_sample = run_command(
            [binary, "sandbox", "exec", run_id, "--", "bash", "-lc", run_script],
            timeout_s,
        )
        exec_sample.metric = "sandbox_exec"
        samples.append(exec_sample)
    else:
        samples.append(
            Sample(
                metric="sandbox_exec",
                duration_ms=0.0,
                rc=1,
                stdout="",
                stderr="skipped (start failed)",
                command=[],
            )
        )

    if create.rc == 0 and started:
        stop = run_command([binary, "sandbox", "stop", run_id], timeout_s)
        stop.metric = "sandbox_stop"
        samples.append(stop)
    else:
        samples.append(
            Sample(
                metric="sandbox_stop",
                duration_ms=0.0,
                rc=1,
                stdout="",
                stderr="skipped (create/start failed)",
                command=[],
            )
        )

    rm = run_command([binary, "sandbox", "rm", run_id], timeout_s)
    rm.metric = "sandbox_rm"
    samples.append(rm)

    total_elapsed = (time.perf_counter() - start_total) * 1000.0
    total_rc = 0 if all(s.rc == 0 for s in samples[:5]) else 1
    samples.append(
        Sample(
            metric="sandbox_total",
            duration_ms=total_elapsed,
            rc=total_rc,
            stdout="",
            stderr="",
            command=[],
        )
    )
    return samples


def run_sandbox_diff_apply(
    binary: str,
    mount_source: str,
    mount_dest: str,
    iteration: int,
    timeout_s: float,
) -> List[Sample]:
    run_id = f"bench-diff-{int(time.time() * 1000)}-{iteration}"
    volume = f"{mount_source}:{mount_dest}:rw:cow=on"
    marker = f".firebox-bench-{int(time.time() * 1000)}-{iteration}.txt"
    marker_script = f'printf "bench\\n" > "{marker}"'
    marker_host_path = os.path.join(mount_source, marker)
    samples: List[Sample] = []
    start_total = time.perf_counter()

    create = run_command(
        [
            binary,
            "sandbox",
            "create",
            "--id",
            run_id,
            "--strict-budget=false",
            "-v",
            volume,
            "-w",
            mount_dest,
        ],
        timeout_s,
    )
    create.metric = "sandbox_diff_create"
    samples.append(create)

    started = False
    if create.rc == 0:
        start = run_command([binary, "sandbox", "start", run_id], timeout_s)
        start.metric = "sandbox_diff_start"
        samples.append(start)
        started = start.rc == 0
    else:
        samples.append(
            Sample(
                metric="sandbox_diff_start",
                duration_ms=0.0,
                rc=1,
                stdout="",
                stderr="skipped (create failed)",
                command=[],
            )
        )

    wrote = False
    if started:
        exec_sample = run_command(
            [binary, "sandbox", "exec", run_id, "--", "bash", "-lc", marker_script],
            timeout_s,
        )
        exec_sample.metric = "sandbox_diff_exec"
        samples.append(exec_sample)
        wrote = exec_sample.rc == 0
    else:
        samples.append(
            Sample(
                metric="sandbox_diff_exec",
                duration_ms=0.0,
                rc=1,
                stdout="",
                stderr="skipped (start failed)",
                command=[],
            )
        )

    if wrote:
        diff_sample = run_command(
            [binary, "sandbox", "diff", run_id, "--path", mount_dest],
            timeout_s,
        )
        diff_sample.metric = "sandbox_diff"
        samples.append(diff_sample)

        apply_sample = run_command(
            [binary, "sandbox", "apply", run_id, "--path", mount_dest],
            timeout_s,
        )
        apply_sample.metric = "sandbox_apply"
        samples.append(apply_sample)
    else:
        samples.append(
            Sample(
                metric="sandbox_diff",
                duration_ms=0.0,
                rc=1,
                stdout="",
                stderr="skipped (exec failed)",
                command=[],
            )
        )
        samples.append(
            Sample(
                metric="sandbox_apply",
                duration_ms=0.0,
                rc=1,
                stdout="",
                stderr="skipped (exec failed)",
                command=[],
            )
        )

    if create.rc == 0 and started:
        stop = run_command([binary, "sandbox", "stop", run_id], timeout_s)
        stop.metric = "sandbox_diff_stop"
        samples.append(stop)
    else:
        samples.append(
            Sample(
                metric="sandbox_diff_stop",
                duration_ms=0.0,
                rc=1,
                stdout="",
                stderr="skipped (create/start failed)",
                command=[],
            )
        )

    rm = run_command([binary, "sandbox", "rm", run_id], timeout_s)
    rm.metric = "sandbox_diff_rm"
    samples.append(rm)

    if os.path.exists(marker_host_path):
        try:
            os.remove(marker_host_path)
        except OSError:
            pass

    total_elapsed = (time.perf_counter() - start_total) * 1000.0
    total_rc = 0 if all(s.rc == 0 for s in samples[:7]) else 1
    samples.append(
        Sample(
            metric="sandbox_diff_total",
            duration_ms=total_elapsed,
            rc=total_rc,
            stdout="",
            stderr="",
            command=[],
        )
    )
    return samples


def build_scenarios(
    binary: str,
    mount_source: str,
    mount_dest: str,
    run_script: str,
    write_script: str,
    selected: Iterable[str],
) -> Dict[str, callable]:
    volume = f"{mount_source}:{mount_dest}:rw:cow=on"
    scenarios = {
        "run_echo": lambda timeout_s, i: run_simple_scenario(
            "run_echo",
            [binary, "run", "--strict-budget=false", "echo", "hi"],
            timeout_s,
        ),
        "run_mount": lambda timeout_s, i: run_simple_scenario(
            "run_mount",
            [
                binary,
                "run",
                "--strict-budget=false",
                "-v",
                volume,
                "-w",
                mount_dest,
                "bash",
                "-lc",
                run_script,
            ],
            timeout_s,
        ),
        "run_mount_write": lambda timeout_s, i: run_simple_scenario(
            "run_mount_write",
            [
                binary,
                "run",
                "--strict-budget=false",
                "-v",
                volume,
                "-w",
                mount_dest,
                "bash",
                "-lc",
                write_script,
            ],
            timeout_s,
        ),
        "sandbox_flow": lambda timeout_s, i: run_sandbox_flow(
            binary,
            mount_source,
            mount_dest,
            run_script,
            i,
            timeout_s,
        ),
        "sandbox_diff_apply": lambda timeout_s, i: run_sandbox_diff_apply(
            binary,
            mount_source,
            mount_dest,
            i,
            timeout_s,
        ),
    }

    missing = [name for name in selected if name not in scenarios]
    if missing:
        raise ValueError(f"unknown scenarios: {', '.join(missing)}")

    return {name: scenarios[name] for name in selected}


def print_results(stats: List[MetricStats], budget_ms: float) -> None:
    headers = [
        "METRIC",
        "N",
        "OK",
        "FAIL",
        "MEAN",
        "P50",
        "P95",
        "P99",
        "MIN",
        "MAX",
        f"P95<={int(budget_ms)}",
    ]
    widths = [18, 5, 5, 5, 8, 8, 8, 8, 8, 8, 11]
    line = "".join(h.ljust(w) for h, w in zip(headers, widths))
    print(line)
    print("-" * len(line))

    for st in stats:
        row = [
            st.metric,
            str(st.count),
            str(st.ok),
            str(st.fail),
            f"{st.mean_ms:.1f}",
            f"{st.p50_ms:.1f}",
            f"{st.p95_ms:.1f}",
            f"{st.p99_ms:.1f}",
            f"{st.min_ms:.1f}",
            f"{st.max_ms:.1f}",
            "PASS" if st.budget_pass(budget_ms) else "FAIL",
        ]
        print("".join(c.ljust(w) for c, w in zip(row, widths)))


def main() -> int:
    parser = argparse.ArgumentParser(description="Benchmark firebox CLI latency.")
    parser.add_argument("--binary", default="./firebox", help="Path to firebox binary")
    parser.add_argument("--iterations", type=int, default=15, help="Measured iterations per scenario")
    parser.add_argument("--warmup", type=int, default=3, help="Warmup iterations per scenario")
    parser.add_argument("--timeout", type=float, default=30.0, help="Per-command timeout in seconds")
    parser.add_argument("--budget-ms", type=float, default=200.0, help="Latency budget for p95 check")
    parser.add_argument(
        "--scenarios",
        default="run_echo,run_mount,sandbox_flow",
        help="Comma-separated scenarios: run_echo,run_mount,run_mount_write,sandbox_flow,sandbox_diff_apply",
    )
    parser.add_argument(
        "--mount-source",
        default="",
        help="Host source path for mount scenarios (default: current working directory)",
    )
    parser.add_argument(
        "--mount-dest",
        default="/workspace",
        help="Guest destination path for mount scenarios",
    )
    parser.add_argument(
        "--run-script",
        default="true",
        help="Shell script for run_mount and sandbox exec scenarios",
    )
    parser.add_argument(
        "--write-script",
        default='f=.cowbench.$$; echo x > "$f"; rm -f "$f"',
        help="Shell script for run_mount_write scenario",
    )
    parser.add_argument("--json-out", default="", help="Write full results to JSON file")
    parser.add_argument("--enforce-budget", action="store_true", help="Exit non-zero if any metric violates budget")
    parser.add_argument(
        "--manage-daemon",
        action=argparse.BooleanOptionalAction,
        default=True,
        help="Start/stop daemon as needed",
    )
    parser.add_argument(
        "--keep-daemon",
        action="store_true",
        help="Do not stop daemon if this script started it",
    )
    args = parser.parse_args()

    if args.iterations <= 0:
        print("error: --iterations must be > 0", file=sys.stderr)
        return 2
    if args.warmup < 0:
        print("error: --warmup must be >= 0", file=sys.stderr)
        return 2
    if not os.path.isfile(args.binary) or not os.access(args.binary, os.X_OK):
        print(f"error: binary not executable: {args.binary}", file=sys.stderr)
        return 2

    cwd = os.getcwd()
    mount_source = args.mount_source if args.mount_source else cwd
    mount_source = os.path.abspath(mount_source)
    if not os.path.exists(mount_source):
        print(f"error: --mount-source path does not exist: {mount_source}", file=sys.stderr)
        return 2
    if not args.mount_dest.startswith("/"):
        print("error: --mount-dest must be absolute (e.g. /workspace)", file=sys.stderr)
        return 2

    selected = [s.strip() for s in args.scenarios.split(",") if s.strip()]
    try:
        scenarios = build_scenarios(
            args.binary,
            mount_source,
            args.mount_dest,
            args.run_script,
            args.write_script,
            selected,
        )
    except ValueError as err:
        print(f"error: {err}", file=sys.stderr)
        return 2

    started_by_script = False
    daemon_samples: List[Sample] = []

    if args.manage_daemon:
        started_by_script, ds = ensure_daemon_started(args.binary, args.timeout)
        daemon_samples.append(ds)
        if ds.rc != 0:
            print("error: failed to ensure daemon is running", file=sys.stderr)
            print(ds.stderr or ds.stdout, file=sys.stderr)
            return 1

    print(
        f"Running benchmark: iterations={args.iterations}, warmup={args.warmup}, "
        f"budget={args.budget_ms:.0f}ms, scenarios={','.join(selected)}"
    )
    print(f"Using binary: {args.binary}")
    print(f"Mount source: {mount_source} -> {args.mount_dest}")

    all_samples: List[Sample] = []

    for name, scenario_fn in scenarios.items():
        for i in range(args.warmup):
            scenario_fn(args.timeout, i)
        for i in range(args.iterations):
            result = scenario_fn(args.timeout, i)
            all_samples.extend(result)

    if args.manage_daemon and started_by_script and not args.keep_daemon:
        stop_sample = stop_daemon(args.binary, args.timeout)
        daemon_samples.append(stop_sample)

    grouped: Dict[str, List[Sample]] = {}
    for sample in all_samples:
        grouped.setdefault(sample.metric, []).append(sample)

    stats = [summarize(metric, samples) for metric, samples in sorted(grouped.items())]
    print("")
    print_results(stats, args.budget_ms)

    failures = [s for s in all_samples if s.rc != 0]
    if failures:
        print(f"\nFailures: {len(failures)}")
        for sample in failures[:10]:
            cmd_str = " ".join(sample.command) if sample.command else "<n/a>"
            msg = sample.stderr.strip() or sample.stdout.strip() or "(no output)"
            print(f"- {sample.metric}: rc={sample.rc}, cmd={cmd_str}")
            print(f"  {msg.splitlines()[0] if msg else '(no output)'}")

    if args.json_out:
        payload = {
            "config": {
                "binary": args.binary,
                "iterations": args.iterations,
                "warmup": args.warmup,
                "timeout_s": args.timeout,
                "budget_ms": args.budget_ms,
                "scenarios": selected,
                "mount_source": mount_source,
                "mount_dest": args.mount_dest,
                "run_script": args.run_script,
                "write_script": args.write_script,
            },
            "stats": [asdict(s) for s in stats],
            "daemon_events": [asdict(s) for s in daemon_samples],
            "samples": [asdict(s) for s in all_samples],
        }
        with open(args.json_out, "w", encoding="utf-8") as f:
            json.dump(payload, f, indent=2)
        print(f"\nWrote JSON report: {args.json_out}")

    budget_failed = [s for s in stats if not s.budget_pass(args.budget_ms)]
    if args.enforce_budget and budget_failed:
        print("\nBudget check failed for:")
        for st in budget_failed:
            print(f"- {st.metric}: p95={st.p95_ms:.1f}ms, fail={st.fail}")
        return 2

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
