#!/usr/bin/env python3
"""Create an S3 bucket with path-style SigV4 (MinIO). List+Put only."""

from __future__ import annotations

import hashlib
import hmac
import sys
import urllib.error
import urllib.request
from datetime import UTC, datetime
from pathlib import Path
from urllib.parse import urlparse


def _hmac(key: bytes, msg: str) -> bytes:
    return hmac.new(key, msg.encode("utf-8"), hashlib.sha256).digest()


def main() -> int:
    if len(sys.argv) != 5:
        print(
            "usage: create-bucket.py ENDPOINT BUCKET ACCESS_FILE SECRET_FILE",
            file=sys.stderr,
        )
        return 2
    endpoint, bucket, access_file, secret_file = sys.argv[1:5]
    access = Path(access_file).read_text(encoding="utf-8").strip()
    secret = Path(secret_file).read_text(encoding="utf-8").strip()
    parsed = urlparse(endpoint)
    host = parsed.netloc
    now = datetime.now(UTC)
    amz = now.strftime("%Y%m%dT%H%M%SZ")
    date = now.strftime("%Y%m%d")
    region = "us-east-1"
    payload_hash = hashlib.sha256(b"").hexdigest()
    canonical = "\n".join(
        [
            "PUT",
            "/" + bucket,
            "",
            f"host:{host}",
            f"x-amz-content-sha256:{payload_hash}",
            f"x-amz-date:{amz}",
            "",
            "host;x-amz-content-sha256;x-amz-date",
            payload_hash,
        ]
    )
    scope = f"{date}/{region}/s3/aws4_request"
    string_to_sign = "\n".join(
        [
            "AWS4-HMAC-SHA256",
            amz,
            scope,
            hashlib.sha256(canonical.encode("utf-8")).hexdigest(),
        ]
    )
    signing_key = _hmac(
        _hmac(_hmac(_hmac(b"AWS4" + secret.encode("utf-8"), date), region), "s3"),
        "aws4_request",
    )
    sig = hmac.new(
        signing_key, string_to_sign.encode("utf-8"), hashlib.sha256
    ).hexdigest()
    req = urllib.request.Request(
        f"{endpoint.rstrip('/')}/{bucket}",
        method="PUT",
        data=b"",
        headers={
            "Host": host,
            "X-Amz-Date": amz,
            "X-Amz-Content-Sha256": payload_hash,
            "Authorization": (
                f"AWS4-HMAC-SHA256 Credential={access}/{scope}, "
                f"SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature={sig}"
            ),
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as res:
            res.read()
            print(f"bucket {bucket} HTTP {res.status}")
            return 0
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        if (
            exc.code in (200, 409)
            or "BucketAlreadyOwnedByYou" in body
            or "BucketAlreadyExists" in body
        ):
            print(f"bucket {bucket} already present ({exc.code})")
            return 0
        print(f"create-bucket HTTP {exc.code}: {body[:400]}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
