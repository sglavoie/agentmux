"""Git and worktree helpers for dashboard session operations."""

import atexit
import concurrent.futures
import os
import subprocess
import threading
import time


class _GitCache:
    """Thread-safe cache for git info with background refresh.

    Returns cached results immediately and schedules background updates
    when the TTL expires, so callers never block on git commands.
    """

    def __init__(self, ttl=10, max_workers=4):
        self._ttl = ttl
        self._lock = threading.Lock()
        # {cwd: {"result": dict|None, "timestamp": float}}
        self._cache: dict[str, dict] = {}
        self._pending: set[str] = set()
        self._pool = concurrent.futures.ThreadPoolExecutor(max_workers=max_workers)

    def get(self, cwd):
        """Return cached git info for *cwd*, or None if not yet available.

        On first call for a given cwd, schedules a background fetch and
        returns None.  On subsequent calls, returns the cached result
        (possibly stale while a refresh is in flight).
        """
        now = time.monotonic()
        with self._lock:
            entry = self._cache.get(cwd)
            if entry is not None:
                age = now - entry["timestamp"]
                if age < self._ttl:
                    return entry["result"]
                # Stale — schedule refresh if not already pending
                if cwd not in self._pending:
                    self._pending.add(cwd)
                    self._pool.submit(self._refresh, cwd)
                return entry["result"]
            # First request — schedule and return None
            if cwd not in self._pending:
                self._pending.add(cwd)
                self._pool.submit(self._refresh, cwd)
            return None

    def _refresh(self, cwd):
        """Fetch git info synchronously and store in cache."""
        result = _get_git_info(cwd)
        with self._lock:
            self._cache[cwd] = {"result": result, "timestamp": time.monotonic()}
            self._pending.discard(cwd)

    def invalidate(self, cwd=None):
        """Clear cache for a specific cwd, or all entries if cwd is None."""
        with self._lock:
            if cwd is None:
                self._cache.clear()
            else:
                self._cache.pop(cwd, None)

    def shutdown(self):
        """Shut down the background thread pool."""
        self._pool.shutdown(wait=False)


# Module-level cache instance used by sessions.py
_git_cache = _GitCache()
atexit.register(_git_cache.shutdown)


def get_git_info_cached(cwd):
    """Non-blocking git info lookup. Returns cached result or None."""
    return _git_cache.get(cwd)


def _get_git_info(cwd):
    """Return git branch and dirty state for a directory, or None if not a git repo.

    Returns {"branch": str, "dirty": bool} on success, None otherwise.
    Each git command has a 2-second timeout to avoid stalling on slow filesystems.
    """
    try:
        result = subprocess.run(
            ["git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD"],
            capture_output=True,
            text=True,
            timeout=2,
        )
        if result.returncode != 0:
            return None
        branch = result.stdout.strip()
    except (subprocess.SubprocessError, OSError):
        return None

    dirty = False
    try:
        result = subprocess.run(
            ["git", "-C", cwd, "status", "--porcelain"],
            capture_output=True,
            text=True,
            timeout=2,
        )
        if result.returncode == 0 and result.stdout.strip():
            dirty = True
    except (subprocess.SubprocessError, OSError):
        pass

    return {"branch": branch, "dirty": dirty}


def do_create_worktree(cwd, branch_name):
    """Create a git worktree for the given branch.

    Returns a dict on success: {"worktree_path": str, "project_name": str}
    Returns a string error message on failure.
    """
    try:
        result = subprocess.run(
            ["git", "-C", cwd, "rev-parse", "--show-toplevel"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode != 0:
            return "Could not determine git root"
        repo_root = result.stdout.strip()
    except (subprocess.SubprocessError, OSError):
        return "Could not determine git root"

    project_name = os.path.basename(repo_root)
    worktree_path = os.path.normpath(
        os.path.join(repo_root, "..", f"{project_name}-{branch_name}")
    )

    branch_exists = False
    try:
        result = subprocess.run(
            ["git", "-C", repo_root, "rev-parse", "--verify", branch_name],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode == 0:
            branch_exists = True
    except (subprocess.SubprocessError, OSError):
        pass

    if not branch_exists:
        try:
            result = subprocess.run(
                [
                    "git",
                    "-C",
                    repo_root,
                    "rev-parse",
                    "--verify",
                    f"origin/{branch_name}",
                ],
                capture_output=True,
                text=True,
                timeout=30,
            )
            if result.returncode == 0:
                branch_exists = True
        except (subprocess.SubprocessError, OSError):
            pass

    if branch_exists:
        cmd = ["git", "-C", repo_root, "worktree", "add", worktree_path, branch_name]
    else:
        cmd = [
            "git",
            "-C",
            repo_root,
            "worktree",
            "add",
            "-b",
            branch_name,
            worktree_path,
        ]

    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
    except (subprocess.SubprocessError, OSError) as exc:
        return str(exc)

    if result.returncode != 0:
        stderr_lines = result.stderr.strip().splitlines()
        for line in reversed(stderr_lines):
            if line.startswith("fatal:"):
                return line
        return stderr_lines[-1] if stderr_lines else "git worktree add failed"

    return {"worktree_path": worktree_path, "project_name": project_name}


def detect_worktree(cwd):
    """Check if a directory is a git worktree (not a normal repo or non-git dir).

    Returns {"is_worktree": bool, "main_repo": str} where main_repo is the
    git common dir path (needed for `git worktree remove`).
    """
    try:
        common = subprocess.run(
            ["git", "-C", cwd, "rev-parse", "--git-common-dir"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        gitdir = subprocess.run(
            ["git", "-C", cwd, "rev-parse", "--git-dir"],
            capture_output=True,
            text=True,
            timeout=10,
        )
    except (subprocess.SubprocessError, OSError):
        return {"is_worktree": False, "main_repo": ""}

    if common.returncode != 0 or gitdir.returncode != 0:
        return {"is_worktree": False, "main_repo": ""}

    common_dir = os.path.realpath(os.path.join(cwd, common.stdout.strip()))
    git_dir = os.path.realpath(os.path.join(cwd, gitdir.stdout.strip()))

    if common_dir != git_dir:
        main_repo = os.path.dirname(common_dir)
        return {"is_worktree": True, "main_repo": main_repo}

    return {"is_worktree": False, "main_repo": ""}


def check_git_safety(path):
    """Check a git repo for uncommitted changes, untracked files, and unpushed commits.

    Returns a list of warning strings. Empty list means the repo is clean.
    Silently skips any check that fails or times out.
    """
    warnings = []

    try:
        result = subprocess.run(
            ["git", "-C", path, "status", "--porcelain"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        if result.returncode == 0 and result.stdout.strip():
            lines = result.stdout.strip().splitlines()
            untracked = [line for line in lines if line.startswith("?? ")]
            changed = [line for line in lines if not line.startswith("?? ")]
            if changed:
                warnings.append(f"Has uncommitted changes ({len(changed)} files)")
            if untracked:
                warnings.append(f"Has untracked files ({len(untracked)} files)")
    except (subprocess.SubprocessError, OSError):
        pass

    try:
        result = subprocess.run(
            ["git", "-C", path, "log", "--oneline", "@{u}..HEAD"],
            capture_output=True,
            text=True,
            timeout=10,
        )
        if result.returncode == 0 and result.stdout.strip():
            count = len(result.stdout.strip().splitlines())
            warnings.append(f"Has unpushed commits ({count} commits)")
        elif result.returncode != 0:
            warnings.append("No remote tracking branch")
    except (subprocess.SubprocessError, OSError):
        pass

    return warnings


def do_remove_worktree(main_repo, worktree_path):
    """Remove a git worktree cleanly. Returns None on success or error string."""
    try:
        result = subprocess.run(
            ["git", "-C", main_repo, "worktree", "remove", worktree_path],
            capture_output=True,
            text=True,
            timeout=30,
        )
    except (subprocess.SubprocessError, OSError) as exc:
        return str(exc)

    if result.returncode != 0:
        stderr = result.stderr.strip()
        return stderr if stderr else "git worktree remove failed"
    return None
