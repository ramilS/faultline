"""Verifies sentry-sdk — the X-Sentry-Auth header auth family — against a
real, running Faultline. run.sh seeds the project/key, starts the server, and
exports the env vars this script reads; it is not runnable standalone.
"""

import os
import subprocess
import sys
import time

import sentry_sdk


def require_env(name: str) -> str:
    value = os.environ.get(name)
    if not value:
        raise SystemExit(f"missing required env var {name}")
    return value


DSN = require_env("FAULTLINE_E2E_DSN")
PROJECT_ID = require_env("FAULTLINE_E2E_PROJECT_ID")
DB_CONTAINER = require_env("FAULTLINE_E2E_DB_CONTAINER")
NONCE = require_env("FAULTLINE_E2E_NONCE")

OUTER_MESSAGE = f"py outer failure {NONCE}"
INNER_MESSAGE = f"py inner cause {NONCE}"
# Faultline's titleFor renders "Type: Value"; RuntimeError is the type of the
# outer `raise ... from` below.
EXPECTED_TITLE = f"RuntimeError: {OUTER_MESSAGE}"

sentry_sdk.init(dsn=DSN, traces_sample_rate=0)


def read_manifest_file() -> None:
    raise ValueError(INNER_MESSAGE)


def parse_manifest() -> None:
    # The throw site: the culprit assertion below must name this function,
    # not start_import (the entry point), proving Faultline reversed
    # Sentry's caller-to-callee frame order.
    try:
        read_manifest_file()
    except ValueError as inner:
        raise RuntimeError(OUTER_MESSAGE) from inner


def start_import() -> None:
    parse_manifest()


def query_issue() -> tuple[str, str] | None:
    # Verification queries Postgres directly rather than the dashboard API —
    # same rationale as the JS check: the dashboard API requires a session
    # cookie, and standing up that login flow would test the wrong thing.
    escaped_title = EXPECTED_TITLE.replace("'", "''")
    sql = (
        "SELECT title || chr(31) || culprit FROM issues "
        f"WHERE project_id = {PROJECT_ID} AND title = '{escaped_title}' "
        "ORDER BY first_seen DESC LIMIT 1;"
    )
    result = subprocess.run(
        ["docker", "exec", "-i", DB_CONTAINER, "psql", "-tA", "-U", "faultline", "-d", "faultline", "-c", sql],
        capture_output=True,
        text=True,
        check=True,
    )
    output = result.stdout.strip()
    if not output:
        return None
    title, _, culprit = output.partition("\x1f")
    return title, culprit


def poll_for_issue(timeout_seconds: float) -> tuple[str, str]:
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        found = query_issue()
        if found:
            return found
        time.sleep(0.5)
    raise SystemExit(f'no issue titled "{EXPECTED_TITLE}" appeared within {timeout_seconds}s')


def main() -> int:
    try:
        start_import()
    except Exception:
        sentry_sdk.capture_exception()

    sentry_sdk.flush()

    title, culprit = poll_for_issue(30)

    # Same unverified assumption as the JS check (exception.values is
    # oldest-first), exercised here via the header auth path instead of the
    # query-string path.
    assert title == EXPECTED_TITLE, f"issue title {title!r} does not name the outer exception"
    assert "parse_manifest" in culprit, f"issue culprit {culprit!r} does not name the throw site"

    print(f"OK: title={title!r} culprit={culprit!r}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
