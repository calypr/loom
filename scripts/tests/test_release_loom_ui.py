import json
import base64
import hashlib
import os
import shutil
import stat
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path
from typing import Optional


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "release-loom-ui.sh"


FAKE_NPM = r'''#!/usr/bin/env python3
import json
import base64
import hashlib
import os
import sys
import tarfile
from io import BytesIO
from pathlib import Path


args = sys.argv[1:]
log_path = Path(os.environ["FAKE_NPM_LOG"])
with log_path.open("a", encoding="utf-8") as handle:
    handle.write(json.dumps(args) + "\n")

if args == ["--version"]:
    print("10.9.4")
    raise SystemExit(0)
if args[:3] == ["config", "get", "registry"]:
    print("https://registry.npmjs.org/")
    raise SystemExit(0)
if args and args[0] == "whoami":
    if os.environ.get("FAKE_AUTH_FAIL") == "1" and not Path(os.environ["FAKE_LOGIN_MARKER"]).exists():
        print("npm error code ENEEDAUTH", file=sys.stderr)
        raise SystemExit(1)
    print("release-bot")
    raise SystemExit(0)
if args and args[0] == "login":
    Path(os.environ["FAKE_LOGIN_MARKER"]).write_text("logged-in", encoding="utf-8")
    raise SystemExit(0)
if args and args[0] == "view":
    if any(argument.startswith("@calypr/loom-ui@") for argument in args):
        if os.environ.get("FAKE_PUBLISHED") == "1" or (
            os.environ.get("FAKE_PUBLISHED_ON_REAL") == "1"
            and Path(os.environ["FAKE_REAL_PUBLISH_MARKER"]).exists()
        ):
            version = next(argument.rsplit("@", 1)[1] for argument in args if argument.startswith("@calypr/loom-ui@"))
            if "dist.integrity" in args:
                tarball = Path(os.environ["FAKE_TARBALL"])
                integrity = "sha512-" + base64.b64encode(hashlib.sha512(tarball.read_bytes()).digest()).decode()
                print(json.dumps(integrity))
                raise SystemExit(0)
            print(json.dumps(version))
            raise SystemExit(0)
        print("npm error code E404", file=sys.stderr)
        raise SystemExit(1)
    repaired_tag = Path(os.environ.get("FAKE_TAG_MARKER", ""))
    if repaired_tag.is_file():
        print(json.dumps(repaired_tag.read_text(encoding="utf-8")))
    elif os.environ.get("FAKE_PUBLISHED_ON_REAL") == "1" and Path(os.environ["FAKE_REAL_PUBLISH_MARKER"]).exists():
        print(json.dumps(os.environ.get("FAKE_PUBLISHED_VERSION", "0.1.2")))
    else:
        print(json.dumps(os.environ.get("FAKE_LATEST", "0.1.1")))
    raise SystemExit(0)
if args and args[0] == "pack":
    package_dir = Path(args[1])
    version = json.loads((package_dir / "package.json").read_text(encoding="utf-8"))["version"]
    destination = Path(args[args.index("--pack-destination") + 1])
    destination.mkdir(parents=True, exist_ok=True)
    tarball = destination / f"calypr-loom-ui-{version}.tgz"
    with tarfile.open(tarball, "w:gz") as archive:
        for name, content in {
            "package/package.json": (package_dir / "package.json").read_bytes(),
            "package/dist/loom-ui.js": b"export {};\n",
            "package/dist/styles.css": b":root {}\n",
        }.items():
            info = tarfile.TarInfo(name)
            info.size = len(content)
            archive.addfile(info, BytesIO(content))
    print(json.dumps([{ "filename": str(tarball) }]))
    raise SystemExit(0)
if args and args[0] == "publish":
    if "--dry-run" not in args:
        if os.environ.get("FAKE_REQUIRE_PUSH") == "1" and not Path(os.environ["FAKE_PUSH_MARKER"]).exists():
            print("publish happened before push", file=sys.stderr)
            raise SystemExit(88)
        Path(os.environ["FAKE_REAL_PUBLISH_MARKER"]).write_text("called", encoding="utf-8")
        if os.environ.get("FAKE_REAL_PUBLISH_SUCCEEDS") == "1":
            raise SystemExit(0)
        raise SystemExit(99)
    raise SystemExit(0)
if args[:2] == ["dist-tag", "add"]:
    package_version = args[2].rsplit("@", 1)[1]
    Path(os.environ["FAKE_TAG_MARKER"]).write_text(package_version, encoding="utf-8")
    raise SystemExit(0)
if args and args[0] in {"ci", "test", "build", "install", "version", "pkg"}:
    raise SystemExit(0)
raise SystemExit(0)
'''


class ReleaseScriptTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory(prefix="loom-release-test-")
        self.root = Path(self.tempdir.name)
        self.script = self.root / "scripts" / "release-loom-ui.sh"
        self.script.parent.mkdir(parents=True)
        shutil.copy2(SCRIPT, self.script)
        self.script.chmod(self.script.stat().st_mode | stat.S_IXUSR)

        loom_ui = self.root / "ui" / "packages" / "loom-ui"
        demo = self.root / "ui" / "apps" / "demo"
        loom_ui.mkdir(parents=True)
        demo.mkdir(parents=True)
        (loom_ui / "package.json").write_text(
            json.dumps({"name": "@calypr/loom-ui", "version": "0.1.2"}), encoding="utf-8"
        )
        (demo / "package.json").write_text(
            json.dumps(
                {
                    "name": "@calypr/loom-demo",
                    "version": "0.1.0",
                    "dependencies": {"@calypr/loom-ui": "0.1.2"},
                }
            ),
            encoding="utf-8",
        )
        (self.root / "ui" / "package.json").write_text(
            json.dumps({"name": "@calypr/loom-ui-workspace", "workspaces": ["packages/loom-ui", "apps/demo"]}),
            encoding="utf-8",
        )
        (self.root / "ui" / "package-lock.json").write_text(
            json.dumps(
                {
                    "name": "@calypr/loom-ui-workspace",
                    "lockfileVersion": 3,
                    "packages": {
                        "apps/demo": {
                            "dependencies": {"@calypr/loom-ui": "0.1.2"}
                        },
                        "packages/loom-ui": {"name": "@calypr/loom-ui", "version": "0.1.2"},
                    },
                }
            ),
            encoding="utf-8",
        )
        (self.root / "Makefile").write_text("release fixture\n", encoding="utf-8")
        fixture_tests = self.root / "scripts" / "tests" / "test_release_loom_ui.py"
        fixture_tests.parent.mkdir(parents=True, exist_ok=True)
        fixture_tests.write_text("release fixture test\n", encoding="utf-8")
        (self.root / ".gitignore").write_text(".artifacts/\n", encoding="utf-8")

        self.fake_bin = self.root / "fake-bin"
        self.fake_bin.mkdir()
        fake_npm = self.fake_bin / "npm"
        fake_npm.write_text(FAKE_NPM, encoding="utf-8")
        fake_npm.chmod(fake_npm.stat().st_mode | stat.S_IXUSR)
        self.npm_log = self.root / "npm.log"
        self.real_publish_marker = self.root / "real-publish-called"
        self.login_marker = self.root / "npm-login-called"
        self.push_marker = self.root / "push-received"
        self.tag_marker = self.root / "tag-repaired"
        self.env = os.environ.copy()
        self.env["PATH"] = f"{self.fake_bin}{os.pathsep}{self.env['PATH']}"
        self.env["FAKE_NPM_LOG"] = str(self.npm_log)
        self.env["FAKE_REAL_PUBLISH_MARKER"] = str(self.real_publish_marker)
        self.env["FAKE_LOGIN_MARKER"] = str(self.login_marker)
        self.env["FAKE_PUSH_MARKER"] = str(self.push_marker)
        self.env["FAKE_TAG_MARKER"] = str(self.tag_marker)
        self.env["FAKE_TARBALL"] = str(self.root / ".artifacts/releases/loom-ui/0.1.2/calypr-loom-ui-0.1.2.tgz")
        self.env["TMPDIR"] = str(self.root)

        subprocess.run(["git", "init", "-q"], cwd=self.root, check=True)
        subprocess.run(["git", "config", "user.email", "release-test@example.invalid"], cwd=self.root, check=True)
        subprocess.run(["git", "config", "user.name", "Release test"], cwd=self.root, check=True)
        subprocess.run(["git", "add", "."], cwd=self.root, check=True)
        subprocess.run(["git", "commit", "-qm", "fixture"], cwd=self.root, check=True)

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def configure_upstream(self, hook: Optional[str] = None) -> Path:
        remote = self.root / "upstream.git"
        subprocess.run(["git", "init", "--bare", "-q", str(remote)], cwd=self.root, check=True)
        subprocess.run(["git", "remote", "add", "origin", str(remote)], cwd=self.root, check=True)
        subprocess.run(["git", "push", "-q", "-u", "origin", "HEAD"], cwd=self.root, check=True)
        if hook is not None:
            receive_hook = remote / "hooks" / "pre-receive"
            receive_hook.write_text(hook, encoding="utf-8")
            receive_hook.chmod(receive_hook.stat().st_mode | stat.S_IXUSR)
        else:
            receive_hook = remote / "hooks" / "post-receive"
            receive_hook.write_text(
                f"#!/bin/sh\nprintf pushed > {self.push_marker!s}\n", encoding="utf-8"
            )
            receive_hook.chmod(receive_hook.stat().st_mode | stat.S_IXUSR)
        return remote

    def commit_pending_owned_change(self) -> None:
        (self.root / "Makefile").write_text("release fixture changed\n", encoding="utf-8")

    def git_output(self, *args: str) -> str:
        return subprocess.check_output(["git", *args], cwd=self.root, text=True).strip()

    def run_script(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [str(self.script), *args],
            cwd=self.root,
            env=self.env,
            text=True,
            capture_output=True,
        )

    def npm_calls(self) -> list[list[str]]:
        if not self.npm_log.exists():
            return []
        return [json.loads(line) for line in self.npm_log.read_text(encoding="utf-8").splitlines()]

    def test_help_and_invalid_version_are_local_and_loud(self) -> None:
        help_result = self.run_script("--help")
        self.assertEqual(help_result.returncode, 0)
        self.assertIn("prepare VERSION", help_result.stdout)

        invalid_result = self.run_script("prepare", "01.2.3")
        self.assertNotEqual(invalid_result.returncode, 0)
        self.assertIn("invalid npm version", invalid_result.stderr)
        self.assertEqual(self.npm_calls(), [])

        prerelease_result = self.run_script("prepare", "0.1.2-rc.1")
        self.assertNotEqual(prerelease_result.returncode, 0)
        self.assertIn("invalid npm version", prerelease_result.stderr)

    def test_prepare_rejects_downgrade_and_stale_latest(self) -> None:
        downgrade_result = self.run_script("prepare", "0.1.1")
        self.assertNotEqual(downgrade_result.returncode, 0)
        self.assertIn("below the checked-in Loom UI version 0.1.2", downgrade_result.stderr)

        publish_downgrade_result = self.run_script("publish", "0.1.1")
        self.assertNotEqual(publish_downgrade_result.returncode, 0)
        self.assertIn("cannot publish 0.1.1 below the checked-in Loom UI version 0.1.2", publish_downgrade_result.stderr)

        self.env["FAKE_LATEST"] = "0.1.2"
        stale_result = self.run_script("prepare", "0.1.2")
        self.assertNotEqual(stale_result.returncode, 0)
        self.assertIn("must be newer than the npm latest version 0.1.2", stale_result.stderr)

        self.env["FAKE_LATEST"] = "0.1.3"
        stale_result = self.run_script("prepare", "0.1.3")
        self.assertNotEqual(stale_result.returncode, 0)
        self.assertIn("must be newer than the npm latest version 0.1.3", stale_result.stderr)

    def test_prepare_only_routes_to_dry_run_publish(self) -> None:
        result = self.run_script("prepare", "0.1.2")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.npm_calls()
        publish_calls = [call for call in calls if call and call[0] == "publish"]
        self.assertEqual(len(publish_calls), 1)
        self.assertIn("--dry-run", publish_calls[0])
        self.assertFalse(self.real_publish_marker.exists())
        self.assertTrue((self.root / ".artifacts/releases/loom-ui/0.1.2/contents.txt").exists())
        self.assertEqual(list(self.root.glob("loom-release-npm-cache.*")), [])

    def test_publish_refuses_dirty_release_before_publish(self) -> None:
        dirty_file = self.root / "ui" / "packages" / "loom-ui" / "README.md"
        dirty_file.write_text("uncommitted change\n", encoding="utf-8")

        result = self.run_script("publish", "0.1.2")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("publish requires committed UI release files", result.stderr)
        self.assertFalse(self.real_publish_marker.exists())
        self.assertFalse(any(call and call[0] == "publish" for call in self.npm_calls()))

    def test_publish_retry_verifies_integrity_without_republishing(self) -> None:
        self.env["FAKE_PUBLISHED"] = "1"
        self.env["FAKE_LATEST"] = "0.1.2"
        result = self.run_script("publish", "0.1.2")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("matches the local tarball", result.stdout)
        self.assertFalse(self.real_publish_marker.exists())
        self.assertFalse(any(call and call[0] == "publish" for call in self.npm_calls()))
        self.assertTrue(any(call[:1] == ["view"] and "dist.integrity" in call for call in self.npm_calls()))

    def test_publish_retry_repairs_an_older_latest_tag(self) -> None:
        self.env["FAKE_PUBLISHED"] = "1"
        self.env["FAKE_LATEST"] = "0.1.1"
        result = self.run_script("publish", "0.1.2")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.tag_marker.read_text(encoding="utf-8"), "0.1.2")
        self.assertFalse(self.real_publish_marker.exists())
        self.assertFalse(any(call and call[0] == "publish" for call in self.npm_calls()))

    def test_release_uses_web_login_when_whoami_fails(self) -> None:
        self.configure_upstream()
        self.env["FAKE_AUTH_FAIL"] = "1"
        self.env["FAKE_PUBLISHED"] = "1"
        self.env["FAKE_LATEST"] = "0.1.2"

        result = self.run_script("release", "0.1.2")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(self.login_marker.exists())
        calls = self.npm_calls()
        whoami = [index for index, call in enumerate(calls) if call[:1] == ["whoami"]]
        login = [index for index, call in enumerate(calls) if call[:1] == ["login"]]
        self.assertGreaterEqual(len(whoami), 2)
        self.assertEqual(len(login), 1)
        self.assertTrue(whoami[0] < login[0] < whoami[-1])
        self.assertIn("--auth-type=web", calls[login[0]])
        self.assertNotIn("OTP", result.stdout + result.stderr)

    def test_release_commits_only_allowlist_and_preserves_index_and_untracked_files(self) -> None:
        self.configure_upstream()
        self.env["FAKE_PUBLISHED"] = "1"
        self.env["FAKE_LATEST"] = "0.1.2"
        self.commit_pending_owned_change()
        staged_unrelated = self.root / "unrelated-staged.txt"
        staged_unrelated.write_text("keep staged\n", encoding="utf-8")
        subprocess.run(["git", "add", str(staged_unrelated)], cwd=self.root, check=True)
        untracked_unrelated = self.root / "unrelated-untracked.txt"
        untracked_unrelated.write_text("keep untracked\n", encoding="utf-8")

        result = self.run_script("release", "0.1.2")

        self.assertEqual(result.returncode, 0, result.stderr)
        committed_paths = self.git_output("show", "--format=", "--name-only", "HEAD").splitlines()
        self.assertEqual(committed_paths, ["Makefile"])
        self.assertEqual(self.git_output("diff", "--cached", "--name-only"), "unrelated-staged.txt")
        self.assertTrue(untracked_unrelated.exists())
        self.assertEqual(self.git_output("rev-parse", "HEAD"), self.git_output("rev-parse", "@{upstream}"))

    def test_release_pushes_before_real_publish(self) -> None:
        self.configure_upstream()
        self.env["FAKE_PUBLISHED_ON_REAL"] = "1"
        self.env["FAKE_REQUIRE_PUSH"] = "1"
        self.env["FAKE_REAL_PUBLISH_SUCCEEDS"] = "1"
        self.env["FAKE_PUBLISHED_VERSION"] = "0.1.2"
        self.env["FAKE_LATEST"] = "0.1.1"
        self.commit_pending_owned_change()

        result = self.run_script("release", "0.1.2")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(self.push_marker.exists())
        self.assertTrue(self.real_publish_marker.exists())

    def test_release_push_failure_prevents_real_publish(self) -> None:
        self.configure_upstream("#!/bin/sh\nexit 1\n")
        self.env["FAKE_PUBLISHED"] = "1"
        self.env["FAKE_LATEST"] = "0.1.2"
        self.commit_pending_owned_change()

        result = self.run_script("release", "0.1.2")

        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.real_publish_marker.exists())
        self.assertFalse(any(call and call[0] == "publish" for call in self.npm_calls()))

    def test_release_rerun_does_not_create_duplicate_commit(self) -> None:
        self.configure_upstream()
        self.env["FAKE_PUBLISHED"] = "1"
        self.env["FAKE_LATEST"] = "0.1.2"
        self.commit_pending_owned_change()

        first = self.run_script("release", "0.1.2")
        first_head = self.git_output("rev-parse", "HEAD")
        first_count = int(self.git_output("rev-list", "--count", "HEAD"))
        second = self.run_script("release", "0.1.2")

        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertEqual(self.git_output("rev-parse", "HEAD"), first_head)
        self.assertEqual(int(self.git_output("rev-list", "--count", "HEAD")), first_count)

    def test_make_release_ui_accepts_positional_version_and_delegates(self) -> None:
        makefile = self.root / "Makefile"
        shutil.copy2(ROOT / "Makefile", makefile)
        marker = self.root / "make-release-args"
        script = self.root / "scripts" / "release-loom-ui.sh"
        script.write_text(
            f"#!/bin/sh\nprintf '%s\\n' \"$@\" > {marker!s}\n", encoding="utf-8"
        )
        script.chmod(script.stat().st_mode | stat.S_IXUSR)

        result = subprocess.run(
            ["make", "release-ui", "0.1.2"],
            cwd=self.root,
            text=True,
            capture_output=True,
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(marker.read_text(encoding="utf-8").splitlines(), ["release", "0.1.2"])

    def test_make_release_ui_rejects_ambiguous_or_missing_versions_before_running(self) -> None:
        makefile = self.root / "Makefile"
        shutil.copy2(ROOT / "Makefile", makefile)
        marker = self.root / "make-release-args"
        script = self.root / "scripts" / "release-loom-ui.sh"
        script.write_text(
            f"#!/bin/sh\nprintf '%s\\n' \"$@\" > {marker!s}\n", encoding="utf-8"
        )
        script.chmod(script.stat().st_mode | stat.S_IXUSR)

        for args in (
            ["release-ui"],
            ["release-ui", "0.1.2", "extra"],
            ["release-ui", "0.1.2", "VERSION=0.2.0"],
        ):
            result = subprocess.run(
                ["make", *args],
                cwd=self.root,
                text=True,
                capture_output=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Usage", result.stderr)
            self.assertFalse(marker.exists())

        inherited = os.environ.copy()
        inherited["VERSION"] = "0.2.0"
        result = subprocess.run(
            ["make", "release-ui", "0.1.2"],
            cwd=self.root,
            env=inherited,
            text=True,
            capture_output=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(marker.read_text(encoding="utf-8").splitlines(), ["release", "0.1.2"])

    def test_release_requires_a_branch_and_upstream_before_authentication(self) -> None:
        missing_upstream = self.run_script("release", "0.1.2")
        self.assertNotEqual(missing_upstream.returncode, 0)
        self.assertIn("configured upstream", missing_upstream.stderr)
        self.assertEqual(self.npm_calls(), [])

        subprocess.run(["git", "checkout", "--detach", "-q"], cwd=self.root, check=True)
        detached = self.run_script("release", "0.1.2")
        self.assertNotEqual(detached.returncode, 0)
        self.assertIn("non-detached branch", detached.stderr)
        self.assertEqual(self.npm_calls(), [])

    def test_release_existing_publication_skips_prepare_mutation_and_verifies_integrity(self) -> None:
        self.configure_upstream()
        self.env["FAKE_PUBLISHED"] = "1"
        self.env["FAKE_LATEST"] = "0.1.2"
        result = self.run_script("release", "0.1.2")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.real_publish_marker.exists())
        calls = self.npm_calls()
        self.assertFalse(any(call[:1] == ["version"] for call in calls))
        self.assertFalse(any(call[:1] == ["pkg"] for call in calls))
        self.assertTrue(any(call[:1] == ["view"] and "dist.integrity" in call for call in calls))

    def test_release_rejects_dirty_ui_source_before_authentication(self) -> None:
        self.configure_upstream()
        dirty_source = self.root / "ui" / "packages" / "loom-ui" / "README.md"
        dirty_source.write_text("not release metadata\n", encoding="utf-8")

        result = self.run_script("release", "0.1.2")

        self.assertNotEqual(result.returncode, 0)
        self.assertIn("outside the release metadata allowlist", result.stderr)
        self.assertEqual(self.npm_calls(), [])


if __name__ == "__main__":
    unittest.main()
