#!/usr/bin/env python3
"""Temporarily disable renderer-selected AQL preview paths 6 through 9."""

import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile


TARGET = Path("internal/dataframe/compiler/render/aql/sequence.go")
MARKER = "const forceGenericRendererAudit = true"


def run_git(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args], text=True).strip()


def repository_root(path):
    return Path(run_git(path, "rev-parse", "--show-toplevel")).resolve()


def backup_directory(root):
    identity = hashlib.sha256(str(root).encode()).hexdigest()[:16]
    return Path(tempfile.gettempdir()) / f"loom-aql-renderer-toggle-{identity}"


def apply_toggle(source):
    if MARKER in source:
        raise ValueError("generic renderer toggle is already present")

    replacements = [
        (
            '''\tif rendered, eligible, err := renderRelatedEligibilityCountRows(plan, sequence, options); err != nil {
\t\treturn RenderedPhysicalPlan{}, err
\t} else if eligible {
\t\treturn rendered, nil
\t}
''',
            '''\tif !forceGenericRendererAudit {
\t\tif rendered, eligible, err := renderRelatedEligibilityCountRows(plan, sequence, options); err != nil {
\t\t\treturn RenderedPhysicalPlan{}, err
\t\t} else if eligible {
\t\t\treturn rendered, nil
\t\t}
\t}
''',
        ),
        (
            '''\tgroupPreviewShape, hasRelatedGroupPreview := relatedGroupPreviewShapeFor(plan, sequence, options)
''',
            '''\tgroupPreviewShape := relatedGroupPreviewShape{}
\thasRelatedGroupPreview := false
\tif !forceGenericRendererAudit {
\t\tgroupPreviewShape, hasRelatedGroupPreview = relatedGroupPreviewShapeFor(plan, sequence, options)
\t}
''',
        ),
        (
            '''\tgroupRowValueWindowPlan, groupRowValueWindowShape, hasGroupRowValueWindow := terminalGroupRowValuePreviewWindowFor(
\t\tplan, sourcePlan, sequence, options, hasRelatedGroupPreview,
\t)
''',
            '''\tgroupRowValueWindowPlan := ir.PhysicalPlan{}
\tgroupRowValueWindowShape := groupRowValuePreviewShape{}
\thasGroupRowValueWindow := false
\tif !forceGenericRendererAudit {
\t\tgroupRowValueWindowPlan, groupRowValueWindowShape, hasGroupRowValueWindow = terminalGroupRowValuePreviewWindowFor(
\t\t\tplan, sourcePlan, sequence, options, hasRelatedGroupPreview,
\t\t)
\t}
''',
        ),
        (
            '''\tif inlineGroupSourceRow != "" {
\t\tsourceGroupWindowPlan, sourceGroupWindowShape, hasSourceGroupWindow = terminalSourceGroupPreviewWindowFor(
\t\t\tplan, sourcePlan, sequence, stages, options, hasRelatedGroupPreview,
\t\t)
\t}
''',
            '''\tif inlineGroupSourceRow != "" && !forceGenericRendererAudit {
\t\tsourceGroupWindowPlan, sourceGroupWindowShape, hasSourceGroupWindow = terminalSourceGroupPreviewWindowFor(
\t\t\tplan, sourcePlan, sequence, stages, options, hasRelatedGroupPreview,
\t\t)
\t}
''',
        ),
    ]
    for old, new in replacements:
        if source.count(old) != 1:
            raise ValueError(f"expected exactly one source match, got {source.count(old)}")
        source = source.replace(old, new, 1)

    marker = "const constructionSourceVariable = "
    if source.count(marker) != 1:
        raise ValueError("could not place renderer audit toggle")
    source = source.replace(marker, MARKER + "\n\n" + marker, 1)
    return source


def state_path(directory):
    return directory / "state.json"


def apply(root):
    target = root / TARGET
    original = target.read_bytes()
    original_text = original.decode()
    toggled = apply_toggle(original_text).encode()

    directory = backup_directory(root)
    directory.mkdir(mode=0o700, parents=True, exist_ok=False)
    (directory / "sequence.original").write_bytes(original)
    (directory / "sequence.toggled").write_bytes(toggled)
    state = {
        "root": str(root),
        "target": str(TARGET),
        "original_sha256": hashlib.sha256(original).hexdigest(),
        "toggled_sha256": hashlib.sha256(toggled).hexdigest(),
        "disabled_paths": [6, 7, 8, 9],
    }
    state_path(directory).write_text(json.dumps(state, indent=2) + "\n")
    target.write_bytes(toggled)
    print(f"disabled renderer-selected AQL preview paths 6–9 in {target}")
    print(f"restore state is outside the repository at {directory}")


def restore(root):
    directory = backup_directory(root)
    state_file = state_path(directory)
    if not state_file.is_file():
        raise ValueError(f"no toggle state found at {directory}")
    state = json.loads(state_file.read_text())
    if state["root"] != str(root):
        raise ValueError("toggle state belongs to another worktree")
    target = root / state["target"]
    current = target.read_bytes()
    actual = hashlib.sha256(current).hexdigest()
    if actual != state["toggled_sha256"]:
        raise ValueError(
            "sequence.go changed after the toggle was applied. "
            "Refusing to overwrite those changes."
        )
    original = (directory / "sequence.original").read_bytes()
    if hashlib.sha256(original).hexdigest() != state["original_sha256"]:
        raise ValueError("saved original source checksum does not match")
    target.write_bytes(original)
    for name in ("sequence.original", "sequence.toggled", "state.json"):
        (directory / name).unlink()
    directory.rmdir()
    print(f"restored {target} byte for byte")


def status(root):
    directory = backup_directory(root)
    state_file = state_path(directory)
    if not state_file.is_file():
        print("renderer toggle is not applied")
        return
    state = json.loads(state_file.read_text())
    current = hashlib.sha256((root / state["target"]).read_bytes()).hexdigest()
    status = "applied" if current == state["toggled_sha256"] else "source changed after apply"
    print(f"renderer toggle is {status} for paths {state['disabled_paths']}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("apply", "restore", "status"))
    parser.add_argument("--root", type=Path, default=Path.cwd(), help="worktree to modify")
    args = parser.parse_args()
    root = repository_root(args.root.resolve())
    {"apply": apply, "restore": restore, "status": status}[args.action](root)


if __name__ == "__main__":
    main()
