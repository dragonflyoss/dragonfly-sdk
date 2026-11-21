"""Command-line entry point for the dragonfly_dfget SDK.

Installed as ``dfget-sdk`` so users can drive the SDK from the shell::

    dfget-sdk download huggingface://Qwen/Qwen2.5-7B/config.json --output ./config.json
    dfget-sdk version
    dfget-sdk health
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import Any, Dict, List, Optional

from .client import DfgetClient, DownloadResult, HealthResult
from .config import DfgetConfig
from .errors import DfgetError


def _add_common_arguments(parser: argparse.ArgumentParser) -> None:
    parser.add_argument(
        "--json",
        dest="json_output",
        action="store_true",
        default=False,
        help="Emit machine-readable JSON output.",
    )
    parser.add_argument(
        "--timeout",
        type=str,
        default="2h",
        help="Download timeout in human-readable duration form (e.g. 2h, 45s, 30m).",
    )
    parser.add_argument(
        "--timeout-s",
        type=float,
        default=None,
        help="Per-command timeout in seconds.",
    )
    parser.add_argument(
        "--dfget-bin",
        default=None,
        help="Path to the dfget binary (overrides DFGET_BIN).",
    )
    parser.add_argument(
        "--endpoint",
        default=None,
        help="Dragonfly dfdaemon endpoint (overrides DFDATON_ENDPOINT).",
    )


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="dfget-sdk",
        description="Dragonfly dfget Python SDK - CLI wrapper.",
    )
    _add_common_arguments(parser)

    sub = parser.add_subparsers(dest="command", required=True)

    download = sub.add_parser("download", help="Download a file via dfget.")
    _add_common_arguments(download)
    download.add_argument("url", help="URL to download, e.g. huggingface://owner/repo/file.")
    download.add_argument(
        "-O",
        "--output",
        required=True,
        help="Specify the output path of the downloaded file.",
    )
    download.add_argument("--overwrite", action="store_true", help="Overwrite the output file.")
    download.add_argument(
        "--force-hard-link",
        action="store_true",
        help="Fail when a hard link cannot be created.",
    )
    download.add_argument(
        "-r",
        "--recursive",
        action="store_true",
        help="Download the directory recursively.",
    )
    download.add_argument("--digest", default=None, help="Integrity digest, e.g. sha256:...")
    download.add_argument(
        "--transfer-from-dfdaemon",
        action="store_true",
        help="Transfer the piece content through the dfdaemon socket.",
    )
    download.add_argument(
        "--content-for-calculating-task-id",
        default=None,
        help="Content used to calculate the task ID.",
    )
    download.add_argument("--tag", default=None, help="Task tag.")
    download.add_argument("--application", default=None, help="Application grouping.")
    download.add_argument(
        "--priority",
        type=int,
        default=None,
        help="Scheduling priority (dfget default: 6).",
    )
    download.add_argument(
        "--background",
        action="store_true",
        default=False,
        help="Request the download in the background.",
    )
    download.add_argument(
        "--extra-arg",
        action="append",
        dest="extra_args",
        default=None,
        help="Additional raw CLI argument passed to dfget (repeatable).",
    )

    version = sub.add_parser("version", help="Print dfget CLI version information.")
    _add_common_arguments(version)

    health = sub.add_parser("health", help="Probe dfget CLI availability.")
    _add_common_arguments(health)

    return parser


def main(argv: Optional[List[str]] = None) -> int:
    """Entry point. Returns the process exit code."""
    args = build_parser().parse_args(argv)

    client = DfgetClient(DfgetConfig(), env={})

    try:
        if args.command == "download":
            result = client.download(
                args.url,
                args.output,
                overwrite=args.overwrite,
                force_hard_link=args.force_hard_link,
                recursive=args.recursive,
                timeout=args.timeout,
                digest=args.digest,
                transfer_from_dfdaemon=args.transfer_from_dfdaemon,
                content_for_calculating_task_id=args.content_for_calculating_task_id,
                tag=args.tag,
                application=args.application,
                priority=args.priority,
                background=args.background or None,
                extra_args=args.extra_args,
                timeout_s=args.timeout_s,
            )
            if args.json_output:
                payload: Dict[str, Any] = {
                    "ok": True,
                    "output_path": result.output_path,
                    "task_id": result.task_id,
                    "downloaded_size": result.downloaded_size,
                    "duration_s": result.duration_s,
                    "returncode": result.returncode,
                }
                print(json.dumps(payload, default=str))
            else:
                print(f"downloaded: {result.output_path}")
                if result.task_id:
                    print(result.task_id)
                print(f"duration:  {result.duration_s:.2f}s")
            return 0

        if args.command == "version":
            version_str = client.version()
            if args.json_output:
                print(json.dumps({"ok": True, "version": version_str}, default=str))
            else:
                print(version_str)
            return 0

        if args.command == "health":
            result = client.health()
            if args.json_output:
                print(
                    json.dumps(
                        {"ok": result.healthy, **result.as_dict()},
                        default=str,
                    )
                )
            else:
                print("healthy" if result.healthy else "unhealthy")
            return 0 if result.healthy else 1
    except DfgetError as exc:
        if args.json_output:
            payload = {
                "ok": False,
                "error": {
                    "type": type(exc).__name__,
                    "returncode": getattr(exc, "returncode", None),
                    "stderr": getattr(exc, "stderr", None),
                },
            }
            print(json.dumps(payload, default=str))
        else:
            print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    import sys

    sys.exit(main())
