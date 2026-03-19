#!/usr/bin/env python3

from __future__ import annotations

import argparse
import sys
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Compile a Light VerSec .trust schema into binary .tlv."
    )
    parser.add_argument(
        "input",
        nargs="?",
        default="repo/config/schema.trust",
        help="Path to the input .trust schema file.",
    )
    parser.add_argument(
        "-o",
        "--output",
        help="Path to the output .tlv file. Defaults to the input path with a .tlv suffix.",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    input_path = Path(args.input).resolve()
    output_path = Path(args.output).resolve() if args.output else input_path.with_suffix(".tlv")

    if not input_path.exists():
        print(f"error: schema file does not exist: {input_path}", file=sys.stderr)
        return 1

    try:
        from ndn.app_support.light_versec import Checker, SemanticError, compile_lvs
    except ImportError as exc:
        print(
            "error: python-ndn is not installed.\n"
            "install it first with: pip install python-ndn",
            file=sys.stderr,
        )
        print(f"detail: {exc}", file=sys.stderr)
        return 2

    trust_text = input_path.read_text(encoding="utf-8")

    try:
        model = compile_lvs(trust_text)
        binary_model = Checker(model, {}).save()
    except SemanticError as exc:
        print(f"error: LVS semantic error in {input_path}:\n{exc}", file=sys.stderr)
        return 3
    except Exception as exc:
        print(f"error: failed to compile {input_path}:\n{exc}", file=sys.stderr)
        return 4

    output_path.write_bytes(binary_model)
    print(f"wrote {len(binary_model)} bytes to {output_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
