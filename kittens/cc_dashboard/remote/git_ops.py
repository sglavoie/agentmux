"""Git and worktree helpers for dashboard session operations."""

import os
import subprocess


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
            untracked = [l for l in lines if l.startswith("?? ")]
            changed = [l for l in lines if not l.startswith("?? ")]
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
