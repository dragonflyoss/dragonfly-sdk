"""Command-line entry point for the dragonfly_dfcache SDK.

Installed as ``dfcache-sdk`` so users can drive the SDK from the shell::

    dfcache-sdk version
    dfcache-sdk health
    dfcache-sdk stat <task-id-or-path>
    dfcache-sdk import <file> --persistent-replica-count 2
    dfcache-sdk export <task-id> --output ./out.bin
    dfcache-sdk delete <task-id>
"""

from __future__ import annotations

import argparse
import json
import sys
from typing import List, Optional

from .client import DfcacheClient
from .errors import DfcacheError


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="dfcache-sdk",
        description="Dragonfly dfcache Python SDK — CLI wrapper.",
    )
    parser.add_argument(
        "--endpoint",
        default=None,
        help="Dragonfly manager/dfdaemon endpoint (overrides DFCACHE_ENDPOINT).",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=None,
        help="Per-command timeout in seconds (default 30).",
    )
    parser.add_argument(
        "--tls",
        action="store_true",
        help="Enable TLS for the connection.",
    )
    parser.add_argument(
        "--tls-ca-cert",
        default=None,
        help="Path to a CA certificate used to verify the peer.",
    )

    sub = parser.add_subparsers(dest="command", required=True)
    stat = sub.add_parser("stat", help="Query cache metadata for a task/path.")
    stat.add_argument("ref", help="Task ID or file path to stat.")

    imp = sub.add_parser("import", help="Import a file into the persistent cache.")
    imp.add_argument("file", help="Local path of the file to import.")
    imp.add_argument(
        "--persistent-replica-count", type=int, default=1, help="Replica count."
    )
    imp.add_argument(
        "--content-for-calculating-task-id",
        default=None,
        help="SHA-256 used to derive the imported task ID.",
    )

    exp = sub.add_parser("export", help="Export a task's content to a local path.")
    exp.add_argument("task_id", help="The task ID to export.")
    exp.add_argument("--output", default=None, help="Destination path.")
    exp.add_argument(
        "--transfer-from-dfdaemon",
        action="store_true",
        help="Request content directly from dfdaemon.",
    )

    dele = sub.add_parser("delete", help="Delete a task from the persistent cache.")
    dele.add_argument("task_id", help="The task ID to delete.")

    sub.add_parser("version", help="Print dfcache CLI version information.")
    sub.add_parser("health", help="Probe dfcache CLI availability.")

    return parser


def main(argv: Optional[List[str]] = None) -> int:
    """Entry point.  Returns the process exit code."""
    args = _build_parser().parse_args(argv)

    from .config import DfcacheConfig  # noqa: PLC2701  (local import keeps CLI light)

    config = DfcacheConfig(
        endpoint=args.endpoint or None,
        tls=args.tls,
        tls_ca_cert=args.tls_ca_cert,
        timeout=args.timeout if args.timeout is not None else 30.0,
    )
    client = DfcacheClient(config)

    try:
        if args.command == "stat":
            _emit(client.stat(args.ref))
        elif args.command == "import":
            _emit(
                client.import_file(
                    args.file,
                    persistent_replica_count=args.persistent_replica_count,
                    content_for_calculating_task_id=(
                        args.content_for_calculating_task_id
                    ),
                )
            )
        elif args.command == "export":
            if not args.output:
                print("export requires --output", file=sys.stderr)
                return 2
            _emit(
                client.export(
                    args.task_id,
                    args.output,
                    transfer_from_dfdaemon=args.transfer_from_dfdaemon,
                )
            )
        elif args.command == "delete":
            _emit(client.delete(args.task_id))
        elif args.command == "version":
            _emit(client.version())
        elif args.command == "health":
            healthy = client.health()
            print("healthy" if healthy else "unhealthy")
            return 0 if healthy else 1
    except DfcacheError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


def _emit(payload) -> None:
    print(json.dumps(payload, ensure_ascii=False, default=str))


if __name__ == "__main__":
    sys.exit(main())