#!/usr/bin/env python3
"""Dispatch the approved test workflow and download its exact artifact safely.

This helper is intentionally limited to the MaestroVPN staging branch and the
non-release ``android-test.yml`` workflow. It never calls Release or OTA APIs.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from pathlib import Path, PurePosixPath, PureWindowsPath
from typing import Any, Callable, Mapping, NamedTuple, Sequence


ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = "evgenmay1978-del/proectmaestro-vpn"
WORKFLOW_FILE = "android-test.yml"
ALLOWED_REF = "codex/mobile-4d-deck"
TASK_ARTIFACT_PATTERNS = {
    "android": "maestrovpn-tv-test-apk",
    "mobile-eye-ring-assets": "mobile-eye-ring-assets-{head_sha}",
    "mobile-eye-runtime-assets": "mobile-eye-runtime-assets-{head_sha}",
}
API_ORIGIN = "https://api.github.com"
API_ACTIONS_PREFIX = f"/repos/{REPOSITORY}/actions/"
HTTP_TIMEOUT_SECONDS = 60
DEFAULT_WAIT_TIMEOUT_SECONDS = 7_200
DEFAULT_POLL_SECONDS = 10.0
STREAM_CHUNK_BYTES = 1024 * 1024
MAX_ARTIFACT_REDIRECTS = 3
ARTIFACT_REDIRECT_STATUSES = frozenset({301, 302, 303, 307, 308})
ARTIFACT_STORAGE_HOSTS = frozenset({"results-receiver.actions.githubusercontent.com"})
ARTIFACT_STORAGE_HOST_SUFFIX = ".blob.core.windows.net"
SHA1_PATTERN = re.compile(r"^[0-9a-f]{40}$")
SAFE_ARTIFACT_NAME = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")
WINDOWS_RESERVED_NAMES = frozenset(
    {"CON", "PRN", "AUX", "NUL"}
    | {f"COM{index}" for index in range(1, 10)}
    | {f"LPT{index}" for index in range(1, 10)}
)


class HelperError(RuntimeError):
    """Base class for expected, safely printable helper failures."""


class PolicyError(HelperError):
    """The requested operation is outside the fixed test-artifact policy."""


class AuthenticationError(HelperError):
    """No usable GitHub API credential was available."""


class GitHubError(HelperError):
    """GitHub Actions did not satisfy the exact run/artifact contract."""


class ArtifactSafetyError(HelperError):
    """An artifact path or archive member was unsafe."""


class LocalRevision(NamedTuple):
    ref: str
    head_sha: str


class RunRecord(NamedTuple):
    run_id: int
    event: str
    head_sha: str
    head_branch: str
    status: str
    conclusion: str | None
    html_url: str


class ArtifactRecord(NamedTuple):
    artifact_id: int
    name: str
    size_in_bytes: int


class ArtifactLimits(NamedTuple):
    max_download_bytes: int
    max_total_extracted_bytes: int
    max_file_bytes: int
    max_members: int
    max_compression_ratio: float


MIB = 1024 * 1024
TASK_ARTIFACT_LIMITS = {
    "android": ArtifactLimits(
        max_download_bytes=256 * MIB,
        max_total_extracted_bytes=256 * MIB,
        max_file_bytes=224 * MIB,
        max_members=16,
        max_compression_ratio=100.0,
    ),
    "mobile-eye-ring-assets": ArtifactLimits(
        max_download_bytes=64 * MIB,
        max_total_extracted_bytes=64 * MIB,
        max_file_bytes=16 * MIB,
        max_members=16,
        max_compression_ratio=100.0,
    ),
    "mobile-eye-runtime-assets": ArtifactLimits(
        max_download_bytes=160 * MIB,
        max_total_extracted_bytes=256 * MIB,
        max_file_bytes=96 * MIB,
        max_members=64,
        max_compression_ratio=100.0,
    ),
}


def artifact_limits_for(task: str) -> ArtifactLimits:
    try:
        return TASK_ARTIFACT_LIMITS[task]
    except KeyError:
        raise PolicyError(f"task is not allowed: {task}") from None


class ArtifactResult(NamedTuple):
    run_directory: Path
    zip_path: Path
    zip_sha256: str
    extracted_directory: Path
    metadata_path: Path
    digest_path: Path


def _validate_ref_and_head(ref: str, head_sha: str) -> None:
    if ref != ALLOWED_REF:
        raise PolicyError(f"ref is not allowed; expected {ALLOWED_REF}")
    if SHA1_PATTERN.fullmatch(head_sha) is None:
        raise PolicyError("HEAD must be an exact 40-character lowercase Git SHA")


def artifact_name_for(task: str, ref: str, head_sha: str) -> str:
    """Return the one exact artifact name permitted for a task."""
    _validate_ref_and_head(ref, head_sha)
    pattern = TASK_ARTIFACT_PATTERNS.get(task)
    if pattern is None:
        raise PolicyError(f"task is not allowed: {task}")
    return pattern.format(head_sha=head_sha)


def validate_actions_api_path(path: str) -> str:
    """Allow only repository-scoped GitHub Actions API paths."""
    if not isinstance(path, str) or not path.startswith("/"):
        raise PolicyError("GitHub API path must be repository-relative")
    decoded_path = urllib.parse.unquote(path.split("?", 1)[0])
    if not decoded_path.startswith(API_ACTIONS_PREFIX):
        raise PolicyError("only repository GitHub Actions endpoints are allowed")
    if ".." in PurePosixPath(decoded_path).parts:
        raise PolicyError("GitHub API path traversal is not allowed")
    return path


def _git_stdout(
    repo_root: Path,
    arguments: Sequence[str],
    *,
    run: Callable[..., Any],
    input_text: str | None = None,
) -> str:
    try:
        completed = run(
            ["git", *arguments],
            cwd=str(repo_root),
            check=True,
            capture_output=True,
            text=True,
            input=input_text,
            timeout=60,
        )
    except Exception as exc:
        raise PolicyError("Git inspection failed") from exc
    return str(completed.stdout)


def read_local_revision(
    repo_root: Path = ROOT,
    *,
    run: Callable[..., Any] = subprocess.run,
) -> LocalRevision:
    """Return a clean local HEAD only when the same SHA is pushed to staging."""
    head_sha = _git_stdout(repo_root, ("rev-parse", "HEAD"), run=run).strip()
    ref = _git_stdout(repo_root, ("branch", "--show-current"), run=run).strip()
    _validate_ref_and_head(ref, head_sha)

    dirty = _git_stdout(repo_root, ("status", "--porcelain"), run=run).strip()
    if dirty:
        raise PolicyError("worktree has uncommitted changes; GitHub would not build them")

    remote_output = _git_stdout(
        repo_root,
        ("ls-remote", "--heads", "origin", f"refs/heads/{ref}"),
        run=run,
    ).strip()
    remote_lines = [line for line in remote_output.splitlines() if line.strip()]
    if len(remote_lines) != 1:
        raise PolicyError("the whitelisted remote ref was not found exactly once")
    remote_fields = remote_lines[0].split()
    remote_sha = remote_fields[0] if remote_fields else ""
    if remote_sha != head_sha:
        raise PolicyError("local HEAD is not pushed to the whitelisted remote ref")
    return LocalRevision(ref=ref, head_sha=head_sha)


def read_github_token(
    environment: Mapping[str, str] | None = None,
    *,
    run: Callable[..., Any] = subprocess.run,
) -> str:
    """Read a token without printing it: environment first, credential helper second."""
    values = os.environ if environment is None else environment
    for name in ("GH_TOKEN", "GITHUB_TOKEN"):
        token = values.get(name, "").strip()
        if token:
            return token

    credential_input = "protocol=https\nhost=github.com\n\n"
    try:
        completed = run(
            ["git", "credential", "fill"],
            input=credential_input,
            check=True,
            capture_output=True,
            text=True,
            timeout=30,
        )
    except Exception as exc:
        raise AuthenticationError(
            "GitHub authentication unavailable; set GH_TOKEN/GITHUB_TOKEN or configure git credential fill"
        ) from exc

    token = ""
    for line in str(completed.stdout).splitlines():
        key, separator, value = line.partition("=")
        if separator and key == "password":
            token = value.strip()
            break
    if not token:
        raise AuthenticationError(
            "GitHub authentication unavailable; set GH_TOKEN/GITHUB_TOKEN or configure git credential fill"
        )
    return token


def redact_sensitive(message: str, secret_values: Sequence[str]) -> str:
    """Remove known secrets and common bearer/password forms from an error."""
    rendered = str(message)
    for secret in sorted({value for value in secret_values if value}, key=len, reverse=True):
        rendered = rendered.replace(secret, "<REDACTED>")
    rendered = re.sub(
        r"(?i)(authorization\s*:\s*bearer\s+)[^\s,;]+",
        r"\1<REDACTED>",
        rendered,
    )
    rendered = re.sub(
        r"(?i)(password\s*=\s*)[^\s,;]+",
        r"\1<REDACTED>",
        rendered,
    )
    return rendered


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Return redirects to the caller instead of following them with API headers."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def _open_without_redirect(request: urllib.request.Request, timeout: float) -> Any:
    opener = urllib.request.build_opener(_NoRedirectHandler())
    return opener.open(request, timeout=timeout)


def validate_artifact_storage_url(url: str) -> str:
    """Accept only HTTPS GitHub Actions storage URLs without user-info or ports."""
    if not isinstance(url, str) or not url:
        raise ArtifactSafetyError("artifact redirect Location is missing")
    parsed = urllib.parse.urlsplit(url)
    try:
        port = parsed.port
    except ValueError:
        raise ArtifactSafetyError("artifact redirect has an invalid port") from None
    host = parsed.hostname.casefold() if parsed.hostname else ""
    try:
        host.encode("ascii")
    except UnicodeEncodeError:
        raise ArtifactSafetyError("artifact redirect host must be ASCII") from None
    allowed_host = host in ARTIFACT_STORAGE_HOSTS or (
        host.endswith(ARTIFACT_STORAGE_HOST_SUFFIX)
        and host != ARTIFACT_STORAGE_HOST_SUFFIX.lstrip(".")
    )
    if (
        parsed.scheme.casefold() != "https"
        or not host
        or parsed.username is not None
        or parsed.password is not None
        or port is not None
        or parsed.fragment
        or not parsed.path.startswith("/")
    ):
        raise ArtifactSafetyError("artifact redirect must be an absolute HTTPS storage URL")
    if not allowed_host:
        raise ArtifactSafetyError("artifact redirect host is not allowed")
    return url


class GitHubActionsApi:
    """Minimal GitHub Actions client with no generic or release endpoint access."""

    def __init__(
        self,
        token: str,
        *,
        opener: Callable[..., Any] | None = None,
    ) -> None:
        if not token:
            raise AuthenticationError("empty GitHub token")
        self._token = token
        self._opener = _open_without_redirect if opener is None else opener

    def _request(
        self,
        method: str,
        path: str,
        payload: Mapping[str, Any] | None = None,
    ) -> Any:
        validate_actions_api_path(path)
        data = None
        if payload is not None:
            data = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        request = urllib.request.Request(
            f"{API_ORIGIN}{path}",
            data=data,
            method=method,
            headers={
                "Accept": "application/vnd.github+json",
                "Authorization": f"Bearer {self._token}",
                "Content-Type": "application/json",
                "User-Agent": "MaestroVPN-GitHub-Actions-Artifact",
                "X-GitHub-Api-Version": "2022-11-28",
            },
        )
        try:
            with self._opener(request, timeout=HTTP_TIMEOUT_SECONDS) as response:
                body = response.read()
                status_code = int(getattr(response, "status", 200))
        except urllib.error.HTTPError as exc:
            raise GitHubError(f"GitHub Actions API returned HTTP {exc.code}") from None
        except urllib.error.URLError:
            raise GitHubError("GitHub Actions API connection failed") from None
        if not 200 <= status_code < 300:
            raise GitHubError(f"GitHub Actions API returned HTTP {status_code}")
        if not body:
            return None
        try:
            return json.loads(body.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError):
            raise GitHubError("GitHub Actions API returned invalid JSON") from None

    def dispatch(self, task: str, ref: str) -> None:
        if ref != ALLOWED_REF:
            raise PolicyError(f"ref is not allowed; expected {ALLOWED_REF}")
        if task not in TASK_ARTIFACT_PATTERNS:
            raise PolicyError(f"task is not allowed: {task}")
        path = f"{API_ACTIONS_PREFIX}workflows/{WORKFLOW_FILE}/dispatches"
        self._request("POST", path, {"ref": ref, "inputs": {"task": task}})

    def list_workflow_runs(self, ref: str) -> Mapping[str, Any]:
        if ref != ALLOWED_REF:
            raise PolicyError(f"ref is not allowed; expected {ALLOWED_REF}")
        query = urllib.parse.urlencode(
            {"branch": ref, "event": "workflow_dispatch", "per_page": 50}
        )
        path = f"{API_ACTIONS_PREFIX}workflows/{WORKFLOW_FILE}/runs?{query}"
        payload = self._request("GET", path)
        if not isinstance(payload, Mapping):
            raise GitHubError("GitHub workflow-runs response is not an object")
        return payload

    def get_run(self, run_id: int) -> Mapping[str, Any]:
        payload = self._request("GET", f"{API_ACTIONS_PREFIX}runs/{int(run_id)}")
        if not isinstance(payload, Mapping):
            raise GitHubError("GitHub run response is not an object")
        return payload

    def list_artifacts(self, run_id: int) -> Mapping[str, Any]:
        payload = self._request(
            "GET", f"{API_ACTIONS_PREFIX}runs/{int(run_id)}/artifacts?per_page=100"
        )
        if not isinstance(payload, Mapping):
            raise GitHubError("GitHub artifacts response is not an object")
        return payload

    def download_artifact(
        self,
        artifact_id: int,
        destination: Path,
        *,
        max_bytes: int,
    ) -> None:
        path = f"{API_ACTIONS_PREFIX}artifacts/{int(artifact_id)}/zip"
        validate_actions_api_path(path)
        if max_bytes <= 0:
            raise ArtifactSafetyError("artifact download limit must be positive")
        if destination.exists():
            raise ArtifactSafetyError(f"refusing to overwrite {destination}")
        destination.parent.mkdir(parents=True, exist_ok=True)
        partial = destination.with_name(f".{destination.name}.part")
        if partial.exists():
            raise ArtifactSafetyError(f"stale partial download exists: {partial}")
        request = urllib.request.Request(
            f"{API_ORIGIN}{path}",
            method="GET",
            headers={
                "Accept": "application/vnd.github+json",
                "Authorization": f"Bearer {self._token}",
                "User-Agent": "MaestroVPN-GitHub-Actions-Artifact",
                "X-GitHub-Api-Version": "2022-11-28",
            },
        )
        try:
            for redirect_count in range(MAX_ARTIFACT_REDIRECTS + 1):
                try:
                    response = self._opener(request, timeout=HTTP_TIMEOUT_SECONDS)
                except urllib.error.HTTPError as exc:
                    if exc.code not in ARTIFACT_REDIRECT_STATUSES:
                        raise
                    response = exc
                with response:
                    status_value = getattr(response, "status", None)
                    if status_value is None:
                        status_value = response.getcode()
                    status_code = int(status_value)
                    if redirect_count == 0 and status_code != 302:
                        raise GitHubError("GitHub artifact download did not return HTTP 302")
                    if status_code in ARTIFACT_REDIRECT_STATUSES:
                        if redirect_count >= MAX_ARTIFACT_REDIRECTS:
                            raise ArtifactSafetyError("artifact redirect limit exceeded")
                        location = response.headers.get("Location", "")
                        storage_url = validate_artifact_storage_url(location)
                        request = urllib.request.Request(
                            storage_url,
                            method="GET",
                            headers={
                                "Accept": "application/zip",
                                "User-Agent": "MaestroVPN-GitHub-Actions-Artifact",
                            },
                        )
                        continue
                    if status_code != 200:
                        raise GitHubError(
                            f"GitHub artifact download returned HTTP {status_code}"
                        )

                    raw_length = response.headers.get("Content-Length")
                    content_length: int | None = None
                    if raw_length is not None:
                        rendered_length = str(raw_length).strip()
                        if re.fullmatch(r"[0-9]+", rendered_length) is None:
                            raise ArtifactSafetyError(
                                "artifact Content-Length is invalid"
                            )
                        content_length = int(rendered_length)
                        if content_length > max_bytes:
                            raise ArtifactSafetyError(
                                "artifact Content-Length exceeds download limit"
                            )

                    bytes_written = 0
                    with partial.open("xb") as output:
                        while True:
                            read_size = min(
                                STREAM_CHUNK_BYTES,
                                max_bytes - bytes_written + 1,
                            )
                            chunk = response.read(read_size)
                            if not chunk:
                                break
                            bytes_written += len(chunk)
                            if bytes_written > max_bytes:
                                raise ArtifactSafetyError(
                                    "artifact download limit exceeded while streaming"
                                )
                            written = output.write(chunk)
                            if written != len(chunk):
                                raise ArtifactSafetyError(
                                    "artifact download produced a short write"
                                )
                    if content_length is not None and bytes_written != content_length:
                        raise ArtifactSafetyError(
                            "artifact download does not match Content-Length"
                        )
                    os.replace(partial, destination)
                    return
            raise ArtifactSafetyError("artifact redirect limit exceeded")
        except urllib.error.HTTPError as exc:
            partial.unlink(missing_ok=True)
            raise GitHubError(f"GitHub artifact download returned HTTP {exc.code}") from None
        except urllib.error.URLError:
            partial.unlink(missing_ok=True)
            raise GitHubError("GitHub artifact download failed") from None
        except Exception:
            partial.unlink(missing_ok=True)
            raise


def _run_record(item: Mapping[str, Any]) -> RunRecord:
    try:
        run_id = int(item["id"])
        event = str(item["event"])
        head_sha = str(item["head_sha"])
        head_branch = str(item["head_branch"])
    except (KeyError, TypeError, ValueError):
        raise GitHubError("GitHub run record is malformed") from None
    conclusion_value = item.get("conclusion")
    return RunRecord(
        run_id=run_id,
        event=event,
        head_sha=head_sha,
        head_branch=head_branch,
        status=str(item.get("status", "")),
        conclusion=None if conclusion_value is None else str(conclusion_value),
        html_url=str(item.get("html_url", "")),
    )


def _matching_runs(
    payload: Mapping[str, Any],
    head_sha: str,
    ref: str,
    previous_ids: set[int],
) -> list[RunRecord]:
    runs = payload.get("workflow_runs", [])
    if not isinstance(runs, list):
        raise GitHubError("GitHub workflow_runs is not a list")
    matches: list[RunRecord] = []
    for item in runs:
        if not isinstance(item, Mapping):
            continue
        record = _run_record(item)
        if (
            record.run_id not in previous_ids
            and record.event == "workflow_dispatch"
            and record.head_sha == head_sha
            and record.head_branch == ref
        ):
            matches.append(record)
    return matches


def matching_run_ids(
    payload: Mapping[str, Any], head_sha: str, ref: str
) -> set[int]:
    return {record.run_id for record in _matching_runs(payload, head_sha, ref, set())}


def select_new_run(
    payload: Mapping[str, Any],
    head_sha: str,
    ref: str,
    previous_ids: set[int],
) -> RunRecord | None:
    """Select exactly one newly dispatched run for the exact local SHA."""
    _validate_ref_and_head(ref, head_sha)
    matches = _matching_runs(payload, head_sha, ref, previous_ids)
    if len(matches) > 1:
        raise GitHubError("multiple new workflow_dispatch runs match the exact HEAD")
    return matches[0] if matches else None


def select_exact_artifact(
    payload: Mapping[str, Any], expected_name: str
) -> ArtifactRecord:
    """Return one non-expired artifact whose name equals the expected name."""
    artifacts = payload.get("artifacts", [])
    if not isinstance(artifacts, list):
        raise GitHubError("GitHub artifacts is not a list")
    matches = [
        item
        for item in artifacts
        if isinstance(item, Mapping)
        and item.get("name") == expected_name
        and item.get("expired") is False
    ]
    if len(matches) != 1:
        raise GitHubError(f"expected one exact artifact named {expected_name}")
    item = matches[0]
    try:
        return ArtifactRecord(
            artifact_id=int(item["id"]),
            name=str(item["name"]),
            size_in_bytes=int(item.get("size_in_bytes", 0)),
        )
    except (KeyError, TypeError, ValueError):
        raise GitHubError("GitHub artifact record is malformed") from None


def wait_for_dispatched_run(
    api: GitHubActionsApi,
    revision: LocalRevision,
    previous_ids: set[int],
    *,
    timeout_seconds: float,
    poll_seconds: float,
    clock: Callable[[], float] = time.monotonic,
    sleep: Callable[[float], None] = time.sleep,
) -> RunRecord:
    deadline = clock() + timeout_seconds
    while True:
        run = select_new_run(
            api.list_workflow_runs(revision.ref),
            revision.head_sha,
            revision.ref,
            previous_ids,
        )
        if run is not None:
            return run
        if clock() >= deadline:
            raise GitHubError("timed out waiting for the exact dispatched workflow run")
        sleep(poll_seconds)


def wait_for_success(
    api: GitHubActionsApi,
    run: RunRecord,
    *,
    timeout_seconds: float,
    poll_seconds: float,
    clock: Callable[[], float] = time.monotonic,
    sleep: Callable[[float], None] = time.sleep,
) -> RunRecord:
    deadline = clock() + timeout_seconds
    while True:
        current = _run_record(api.get_run(run.run_id))
        if (
            current.run_id != run.run_id
            or current.event != "workflow_dispatch"
            or current.head_sha != run.head_sha
            or current.head_branch != run.head_branch
        ):
            raise GitHubError("workflow run identity changed while waiting")
        if current.status == "completed":
            if current.conclusion != "success":
                raise GitHubError(
                    f"workflow run {current.run_id} completed with {current.conclusion or 'no conclusion'}"
                )
            return current
        if clock() >= deadline:
            raise GitHubError(f"timed out waiting for workflow run {run.run_id}")
        sleep(poll_seconds)


def _safe_archive_members(
    archive: zipfile.ZipFile,
    limits: ArtifactLimits,
) -> list[tuple[zipfile.ZipInfo, tuple[str, ...]]]:
    members = archive.infolist()
    if len(members) > limits.max_members:
        raise ArtifactSafetyError("ZIP member count exceeds task limit")

    safe: list[tuple[zipfile.ZipInfo, tuple[str, ...]]] = []
    seen: set[str] = set()
    metadata_total = 0
    for member in members:
        raw_name = member.filename
        if not raw_name or "\x00" in raw_name:
            raise ArtifactSafetyError("ZIP contains an empty or NUL path")
        windows_path = PureWindowsPath(raw_name)
        normalized = raw_name.replace("\\", "/")
        posix_path = PurePosixPath(normalized)
        parts = tuple(posix_path.parts)
        unsafe_windows_component = any(
            part.rstrip(" .") != part
            or ":" in part
            or any(ord(character) < 32 for character in part)
            or part.split(".", 1)[0].upper() in WINDOWS_RESERVED_NAMES
            for part in parts
        )
        if (
            windows_path.is_absolute()
            or bool(windows_path.drive)
            or normalized.startswith("/")
            or any(part in ("", ".", "..") for part in parts)
            or unsafe_windows_component
        ):
            raise ArtifactSafetyError(f"unsafe ZIP member path: {raw_name!r}")
        mode = (member.external_attr >> 16) & 0xFFFF
        if stat.S_ISLNK(mode):
            raise ArtifactSafetyError(f"ZIP symlink is not allowed: {raw_name!r}")
        key = "/".join(parts).casefold()
        if key in seen:
            raise ArtifactSafetyError(f"duplicate ZIP member path: {raw_name!r}")
        seen.add(key)

        if member.file_size < 0 or member.compress_size < 0:
            raise ArtifactSafetyError("ZIP contains an invalid member size")
        if member.file_size > limits.max_file_bytes:
            raise ArtifactSafetyError("ZIP member exceeds per-file task limit")
        metadata_total += member.file_size
        if metadata_total > limits.max_total_extracted_bytes:
            raise ArtifactSafetyError("ZIP total extracted size exceeds task limit")
        if member.file_size:
            compression_ratio = member.file_size / max(member.compress_size, 1)
            if compression_ratio > limits.max_compression_ratio:
                raise ArtifactSafetyError("ZIP member compression ratio exceeds task limit")
        safe.append((member, parts))
    return safe


def safe_extract_zip(
    archive_path: Path,
    destination: Path,
    limits: ArtifactLimits,
) -> None:
    """Extract within task byte/member/ratio caps and safe relative paths."""
    archive_path = Path(archive_path)
    destination = Path(destination)
    if destination.exists():
        raise ArtifactSafetyError(f"refusing to overwrite extraction directory {destination}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(
        tempfile.mkdtemp(prefix=f".{destination.name}-", dir=str(destination.parent))
    )
    total_written = 0
    try:
        with zipfile.ZipFile(archive_path, "r") as archive:
            members = _safe_archive_members(archive, limits)
            for member, parts in members:
                target = temporary.joinpath(*parts)
                if member.is_dir():
                    target.mkdir(parents=True, exist_ok=True)
                    continue
                target.parent.mkdir(parents=True, exist_ok=True)
                member_read = 0
                member_written = 0
                with archive.open(member, "r") as source, target.open("xb") as output:
                    while True:
                        read_size = min(
                            STREAM_CHUNK_BYTES,
                            limits.max_file_bytes - member_read + 1,
                            limits.max_total_extracted_bytes - total_written + 1,
                        )
                        chunk = source.read(read_size)
                        if not chunk:
                            break
                        member_read += len(chunk)
                        if (
                            member_read > limits.max_file_bytes
                            or total_written + len(chunk)
                            > limits.max_total_extracted_bytes
                        ):
                            raise ArtifactSafetyError(
                                "ZIP stream exceeds extracted byte limit"
                            )
                        written = output.write(chunk)
                        if written != len(chunk):
                            raise ArtifactSafetyError("ZIP stream produced a short write")
                        member_written += written
                        total_written += written
                if member_read != member.file_size or member_written != member.file_size:
                    raise ArtifactSafetyError(
                        "ZIP member stream size does not match metadata"
                    )
        os.replace(temporary, destination)
    except Exception:
        shutil.rmtree(temporary, ignore_errors=True)
        raise


def _sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        while True:
            chunk = source.read(1024 * 1024)
            if not chunk:
                break
            digest.update(chunk)
    return digest.hexdigest()


def persist_artifact(
    api: Any,
    run: RunRecord,
    artifact: ArtifactRecord,
    revision: LocalRevision,
    task: str,
    build_root: Path = ROOT / "build/github-artifacts",
) -> ArtifactResult:
    expected_name = artifact_name_for(task, revision.ref, revision.head_sha)
    limits = artifact_limits_for(task)
    if artifact.name != expected_name or SAFE_ARTIFACT_NAME.fullmatch(artifact.name) is None:
        raise ArtifactSafetyError("artifact name does not match the approved task")
    if artifact.size_in_bytes < 0 or artifact.size_in_bytes > limits.max_download_bytes:
        raise ArtifactSafetyError("artifact declared size exceeds task download limit")
    if run.event != "workflow_dispatch" or run.head_sha != revision.head_sha:
        raise ArtifactSafetyError("run does not match the exact local HEAD")
    if run.status != "completed" or run.conclusion != "success":
        raise ArtifactSafetyError("artifact run is not completed/success")

    build_root = Path(build_root)
    build_root.mkdir(parents=True, exist_ok=True)
    run_directory = build_root / f"run-{run.run_id}"
    run_directory.mkdir(exist_ok=False)
    zip_path = run_directory / f"{artifact.name}.zip"
    api.download_artifact(
        artifact.artifact_id,
        zip_path,
        max_bytes=limits.max_download_bytes,
    )
    zip_sha256 = _sha256_file(zip_path)
    extracted_directory = run_directory / "extracted"
    safe_extract_zip(zip_path, extracted_directory, limits)

    digest_path = run_directory / "artifact.sha256"
    digest_path.write_text(f"{zip_sha256}  {zip_path.name}\n", encoding="utf-8")
    metadata_path = run_directory / "metadata.json"
    metadata = {
        "repository": REPOSITORY,
        "workflow": WORKFLOW_FILE,
        "task": task,
        "ref": revision.ref,
        "head_sha": revision.head_sha,
        "run": {
            "id": run.run_id,
            "event": run.event,
            "status": run.status,
            "conclusion": run.conclusion,
            "html_url": run.html_url,
        },
        "artifact": {
            "id": artifact.artifact_id,
            "name": artifact.name,
            "github_size_in_bytes": artifact.size_in_bytes,
            "zip_filename": zip_path.name,
            "zip_sha256": zip_sha256,
            "extracted_directory": extracted_directory.name,
            "safety_limits": dict(limits._asdict()),
        },
    }
    metadata_path.write_text(
        json.dumps(metadata, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    return ArtifactResult(
        run_directory=run_directory,
        zip_path=zip_path,
        zip_sha256=zip_sha256,
        extracted_directory=extracted_directory,
        metadata_path=metadata_path,
        digest_path=digest_path,
    )


def _positive_float(value: str) -> float:
    parsed = float(value)
    if parsed <= 0:
        raise argparse.ArgumentTypeError("must be greater than zero")
    return parsed


def _argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description=(
            "Dispatch the fixed non-release android-test.yml workflow for the exact "
            "pushed local HEAD, wait for success, and safely download its exact artifact."
        )
    )
    parser.add_argument(
        "--task",
        choices=tuple(TASK_ARTIFACT_PATTERNS),
        default="android",
        help="approved manual workflow task (default: android)",
    )
    parser.add_argument(
        "--timeout-seconds",
        type=_positive_float,
        default=float(DEFAULT_WAIT_TIMEOUT_SECONDS),
    )
    parser.add_argument(
        "--poll-seconds",
        type=_positive_float,
        default=DEFAULT_POLL_SECONDS,
    )
    return parser


def run_command(args: argparse.Namespace) -> ArtifactResult:
    revision = read_local_revision(ROOT)
    expected_artifact = artifact_name_for(args.task, revision.ref, revision.head_sha)
    token = read_github_token()
    api = GitHubActionsApi(token)

    previous_ids = matching_run_ids(
        api.list_workflow_runs(revision.ref), revision.head_sha, revision.ref
    )
    api.dispatch(args.task, revision.ref)
    queued_run = wait_for_dispatched_run(
        api,
        revision,
        previous_ids,
        timeout_seconds=args.timeout_seconds,
        poll_seconds=args.poll_seconds,
    )
    successful_run = wait_for_success(
        api,
        queued_run,
        timeout_seconds=args.timeout_seconds,
        poll_seconds=args.poll_seconds,
    )
    artifact = select_exact_artifact(
        api.list_artifacts(successful_run.run_id), expected_artifact
    )
    return persist_artifact(api, successful_run, artifact, revision, args.task)


def main(argv: Sequence[str] | None = None) -> int:
    parser = _argument_parser()
    args = parser.parse_args(argv)
    secret_values = [
        value
        for value in (os.environ.get("GH_TOKEN", ""), os.environ.get("GITHUB_TOKEN", ""))
        if value
    ]
    try:
        result = run_command(args)
    except KeyboardInterrupt:
        print("ERROR: interrupted", file=sys.stderr)
        return 130
    except HelperError as exc:
        print(f"ERROR: {redact_sensitive(str(exc), secret_values)}", file=sys.stderr)
        return 1
    except Exception:
        print("ERROR: unexpected helper failure (details suppressed)", file=sys.stderr)
        return 1

    metadata = json.loads(result.metadata_path.read_text(encoding="utf-8"))
    print("PASS GitHub Actions artifact")
    print(f"run_id={metadata['run']['id']}")
    print(f"run_url={metadata['run']['html_url']}")
    print(f"head_sha={metadata['head_sha']}")
    print(f"artifact={metadata['artifact']['name']}")
    print(f"zip_sha256={result.zip_sha256}")
    print(f"directory={result.run_directory}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
