#!/usr/bin/env python3
"""Firebox benchmark suite (igloo-style output)."""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
import time
from typing import List, Tuple

# ANSI colors
GREEN = "\033[92m"
RED = "\033[91m"
YELLOW = "\033[93m"
BOLD = "\033[1m"
DIM = "\033[2m"
RESET = "\033[0m"

DEFAULT_TEST_DIR = os.path.join(os.path.expanduser("~"), ".firebox-benchmark-test")


def run_command(
    cmd: List[str], timeout: int = 60, expect_fail: bool = False
) -> Tuple[bool, float, str, int]:
    start = time.perf_counter()
    try:
        proc = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
        elapsed = time.perf_counter() - start
        output = (proc.stdout or "") + (proc.stderr or "")
        if expect_fail:
            success = proc.returncode != 0
        else:
            success = proc.returncode == 0
        return success, elapsed, output.strip(), proc.returncode
    except subprocess.TimeoutExpired:
        elapsed = time.perf_counter() - start
        return False, elapsed, "TIMEOUT", 124
    except Exception as exc:  # pragma: no cover
        elapsed = time.perf_counter() - start
        return False, elapsed, str(exc), 1


def print_result(
    description: str,
    success: bool,
    elapsed: float,
    output: str = "",
    expected_fail: bool = False,
) -> None:
    if success:
        status = f"{GREEN}PASS{RESET}"
    elif expected_fail:
        status = f"{YELLOW}EXPECTED{RESET}"
    else:
        status = f"{RED}FAIL{RESET}"
    print(f"  {status:>20}  {elapsed:>8.3f}s  {description}")
    if not success and not expected_fail and output:
        first = output.splitlines()[0][:110]
        print(f"           {RED}> {first}{RESET}")


def print_section(title: str) -> None:
    print(f"\n{BOLD}{title}{RESET}")
    print("-" * 60)


def run_test(
    results: list[tuple[str, bool, float]],
    cmd: List[str],
    description: str,
    timeout: int = 60,
    expected_fail: bool = False,
    expect_fail: bool = False,
) -> tuple[bool, str, int]:
    success, elapsed, output, rc = run_command(cmd, timeout=timeout, expect_fail=expect_fail)
    actual_success = success or expected_fail
    print_result(description, success, elapsed, output, expected_fail=expected_fail)
    results.append((description, actual_success, elapsed))
    return success, output, rc


def setup_test_files(mount_source: str) -> None:
    os.makedirs(mount_source, exist_ok=True)
    with open(os.path.join(mount_source, "test.txt"), "w", encoding="utf-8") as f:
        f.write("firebox benchmark test file\n")
    with open(os.path.join(mount_source, "config.json"), "w", encoding="utf-8") as f:
        f.write('{"benchmark": true, "version": 1}\n')
    with open(os.path.join(mount_source, "script.py"), "w", encoding="utf-8") as f:
        f.write('print("firebox-benchmark")\n')


def cleanup_sandboxes(binary: str, sandbox_ids: list[str]) -> None:
    for sandbox_id in sandbox_ids:
        subprocess.run([binary, "sandbox", "stop", sandbox_id], capture_output=True, text=True, check=False)
        subprocess.run([binary, "sandbox", "rm", sandbox_id], capture_output=True, text=True, check=False)


def daemon_running(binary: str, timeout: int) -> bool:
    success, _, _, _ = run_command([binary, "daemon", "status"], timeout=timeout)
    return success


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Firebox benchmark suite (igloo-style).")
    parser.add_argument("--binary", default="./firebox", help="Path to firebox binary")
    parser.add_argument("--timeout", type=int, default=60, help="Per-command timeout in seconds")
    parser.add_argument(
        "--mount-source",
        default="",
        help=f"Host path to mount (default: {DEFAULT_TEST_DIR})",
    )
    parser.add_argument("--mount-dest", default="/workspace", help="Guest mount destination")
    parser.add_argument("--with-setup", action="store_true", help="Run `firebox setup` at the start")
    parser.add_argument("--setup-name", default="devyaml", help="Image name for setup command")
    parser.add_argument("--setup-file", default="", help="Optional Lima YAML for setup command")
    parser.add_argument("--keep-daemon", action="store_true", help="Do not stop daemon if script started it")
    parser.add_argument("--keep-artifacts", action="store_true", help="Keep benchmark files on disk")
    return parser.parse_args()


def main() -> int:
    args = parse_args()

    mount_source_provided = bool(args.mount_source.strip())
    mount_source = (
        os.path.abspath(args.mount_source.strip()) if mount_source_provided else os.path.abspath(DEFAULT_TEST_DIR)
    )
    mount_dest = args.mount_dest.strip()

    if not mount_dest.startswith("/"):
        print("error: --mount-dest must be an absolute path (example: /workspace)", file=sys.stderr)
        return 2
    if not os.path.isfile(args.binary) or not os.access(args.binary, os.X_OK):
        print(f"error: binary not executable: {args.binary}", file=sys.stderr)
        return 2

    print(f"\n{BOLD}{'=' * 60}{RESET}")
    print(f"{BOLD}{'FIREBOX BENCHMARK SUITE':^60}{RESET}")
    print(f"{BOLD}{'=' * 60}{RESET}\n")

    help_ok, _, help_output, _ = run_command([args.binary, "--help"], timeout=args.timeout)
    if not help_ok:
        print(f"{RED}Error: failed to run {args.binary} --help{RESET}")
        if help_output:
            print(help_output.splitlines()[0])
        return 1

    print(f"  Binary:      {BOLD}{args.binary}{RESET}")
    print(f"  Mount:       {mount_source} -> {mount_dest}")
    print(f"  Timeout:     {args.timeout}s")
    print(f"  With setup:  {'yes' if args.with_setup else 'no'}")
    print()

    results: list[tuple[str, bool, float]] = []
    sandbox_ids: list[str] = []
    daemon_started_by_script = False

    try:
        setup_test_files(mount_source)

        if args.with_setup:
            print_section("Setup")
            setup_cmd = [args.binary, "setup", "--name", args.setup_name]
            if args.setup_file.strip():
                setup_cmd += ["--file", args.setup_file.strip()]
            ok, _, _ = run_test(results, setup_cmd, "firebox setup", timeout=max(args.timeout, 120))
            if not ok:
                print(f"\n{RED}Cannot continue - setup failed{RESET}")
                return 1

        print_section("Daemon")
        was_running = daemon_running(args.binary, args.timeout)
        if was_running:
            run_test(results, [args.binary, "daemon", "status"], "firebox daemon status (already running)")
        else:
            ok, _, _ = run_test(results, [args.binary, "daemon", "start"], "firebox daemon start")
            if not ok:
                print(f"\n{RED}Cannot continue - daemon failed to start{RESET}")
                return 1
            daemon_started_by_script = True
            run_test(results, [args.binary, "daemon", "status"], "firebox daemon status")

        print_section("Basic Commands")
        run_test(results, [args.binary, "--help"], "firebox --help")
        run_test(results, [args.binary, "run", "--help"], "firebox run --help")
        run_test(results, [args.binary, "sandbox", "--help"], "firebox sandbox --help")
        run_test(results, [args.binary, "image", "list"], "firebox image list")
        run_test(results, [args.binary, "metrics"], "firebox metrics")

        print_section("Run Commands")
        volume = f"{mount_source}:{mount_dest}:rw:cow=on"
        run_test(
            results,
            [args.binary, "run", "--strict-budget=false", "echo", "hello"],
            "firebox run echo hello",
        )
        run_test(
            results,
            [
                args.binary,
                "run",
                "--strict-budget=false",
                "-v",
                volume,
                "-w",
                mount_dest,
                "bash",
                "-lc",
                "cat test.txt >/dev/null",
            ],
            "firebox run -v ... cat test.txt",
        )
        run_test(
            results,
            [
                args.binary,
                "run",
                "--strict-budget=false",
                "-v",
                volume,
                "-w",
                mount_dest,
                "bash",
                "-lc",
                "f=.bench-cow.$$.txt; echo bench > \"$f\"; rm -f \"$f\"",
            ],
            "firebox run -v ... write/delete (CoW on)",
        )

        print_section("Sandbox Flow")
        sandbox_id = f"bench-firebox-{int(time.time())}"
        sandbox_ids.append(sandbox_id)
        marker_name = f".bench-apply-{int(time.time() * 1000)}.txt"
        marker_host_path = os.path.join(mount_source, marker_name)

        ok_create, _, _ = run_test(
            results,
            [
                args.binary,
                "sandbox",
                "create",
                "--id",
                sandbox_id,
                "--strict-budget=false",
                "-v",
                volume,
                "-w",
                mount_dest,
            ],
            "firebox sandbox create",
        )
        if ok_create:
            ok_start, _, _ = run_test(
                results,
                [args.binary, "sandbox", "start", sandbox_id],
                "firebox sandbox start",
            )
        else:
            ok_start = False

        if ok_create and ok_start:
            run_test(
                results,
                [
                    args.binary,
                    "sandbox",
                    "exec",
                    sandbox_id,
                    "--",
                    "bash",
                    "-lc",
                    f'echo "bench" > "{marker_name}"',
                ],
                "firebox sandbox exec write marker",
            )
            run_test(
                results,
                [args.binary, "sandbox", "diff", sandbox_id, "--path", mount_dest],
                "firebox sandbox diff --path",
            )
            ok_apply, _, _ = run_test(
                results,
                [args.binary, "sandbox", "apply", sandbox_id, "--path", mount_dest],
                "firebox sandbox apply --path",
            )
            run_test(
                results,
                [args.binary, "sandbox", "inspect", sandbox_id],
                "firebox sandbox inspect",
            )

            check_start = time.perf_counter()
            host_applied = ok_apply and os.path.exists(marker_host_path)
            check_elapsed = time.perf_counter() - check_start
            print_result("host file exists after apply", host_applied, check_elapsed)
            results.append(("host file exists after apply", host_applied, check_elapsed))

        run_test(results, [args.binary, "sandbox", "stop", sandbox_id], "firebox sandbox stop")
        ok_rm, _, _ = run_test(results, [args.binary, "sandbox", "rm", sandbox_id], "firebox sandbox rm")
        if ok_rm and sandbox_id in sandbox_ids:
            sandbox_ids.remove(sandbox_id)

        print_section("Final Metrics")
        run_test(results, [args.binary, "metrics"], "firebox metrics (post-run)")

        if os.path.exists(marker_host_path):
            try:
                os.remove(marker_host_path)
            except OSError:
                pass

    finally:
        cleanup_sandboxes(args.binary, sandbox_ids)
        if daemon_started_by_script and not args.keep_daemon:
            run_command([args.binary, "daemon", "stop"], timeout=args.timeout)
        if not args.keep_artifacts and not mount_source_provided:
            shutil.rmtree(mount_source, ignore_errors=True)

    print(f"\n{BOLD}{'=' * 60}{RESET}")
    print(f"{BOLD}{'SUMMARY':^60}{RESET}")
    print(f"{BOLD}{'=' * 60}{RESET}\n")

    passed = sum(1 for _, success, _ in results if success)
    failed = len(results) - passed
    total_time = sum(elapsed for _, _, elapsed in results)

    print(f"  Total tests:  {len(results)}")
    print(f"  Passed:       {GREEN}{passed}{RESET}")
    if failed > 0:
        print(f"  Failed:       {RED}{failed}{RESET}")
    else:
        print(f"  Failed:       {failed}")
    print(f"  Total time:   {total_time:.3f}s")
    print()

    if failed > 0:
        print(f"  {RED}Failed tests:{RESET}")
        for desc, success, _ in results:
            if not success:
                print(f"    - {desc}")
        print()

    return 0 if failed == 0 else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print(f"\n{YELLOW}Interrupted{RESET}")
        sys.exit(130)
