import os
import pathlib
import subprocess
import tempfile
import textwrap
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[2]


class DemoConfigurationTest(unittest.TestCase):
    def test_demo_up_uses_custom_project_ports_and_dataset(self):
        with tempfile.TemporaryDirectory() as temporary:
            temporary_path = pathlib.Path(temporary)
            log = temporary_path / "docker.log"
            self._write_executable(
                temporary_path / "docker",
                """
                #!/usr/bin/env bash
                printf '%s\n' "$*" >> "$DEMO_TEST_DOCKER_LOG"
                """,
            )
            self._write_executable(temporary_path / "curl", "#!/usr/bin/env bash\nexit 0\n")
            environment = os.environ.copy()
            environment.update(
                {
                    "PATH": f"{temporary_path}:{environment['PATH']}",
                    "DEMO_TEST_DOCKER_LOG": str(log),
                    "LOOM_DEMO_COMPOSE_PROJECT": "alternate-demo",
                    "LOOM_DEMO_API_HOST": "127.0.0.2",
                    "LOOM_DEMO_API_PORT": "18080",
                    "LOOM_DEMO_UI_HOST": "127.0.0.3",
                    "LOOM_DEMO_UI_PORT": "13080",
                    "LOOM_DEMO_PROJECT": "EXAMPLE_PROJECT",
                    "LOOM_DEMO_GENERATION": "example-generation",
                }
            )

            result = subprocess.run(
                [str(ROOT / "scripts/demo-up.sh")],
                cwd=ROOT,
                env=environment,
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            commands = log.read_text()
            self.assertIn("compose --project-name alternate-demo up --build -d arangodb clickhouse loom-api", commands)
            self.assertIn("compose --project-name alternate-demo run --rm --no-deps demo-seed", commands)
            self.assertIn("compose --project-name alternate-demo up --build -d loom-ui", commands)
            self.assertIn("http://127.0.0.3:13080", result.stdout)

    def test_demo_down_uses_custom_project(self):
        with tempfile.TemporaryDirectory() as temporary:
            temporary_path = pathlib.Path(temporary)
            log = temporary_path / "docker.log"
            self._write_executable(
                temporary_path / "docker",
                """
                #!/usr/bin/env bash
                printf '%s\n' "$*" >> "$DEMO_TEST_DOCKER_LOG"
                """,
            )
            environment = os.environ.copy()
            environment.update(
                {
                    "PATH": f"{temporary_path}:{environment['PATH']}",
                    "DEMO_TEST_DOCKER_LOG": str(log),
                    "LOOM_DEMO_COMPOSE_PROJECT": "alternate-demo",
                }
            )

            result = subprocess.run(
                [str(ROOT / "scripts/demo-down.sh"), "--volumes"],
                cwd=ROOT,
                env=environment,
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(log.read_text().strip(), "compose --project-name alternate-demo down --volumes")

    def test_demo_up_brackets_ipv6_urls(self):
        with tempfile.TemporaryDirectory() as temporary:
            temporary_path = pathlib.Path(temporary)
            log = temporary_path / "docker.log"
            self._write_executable(temporary_path / "docker", "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> \"$DEMO_TEST_DOCKER_LOG\"\n")
            self._write_executable(temporary_path / "curl", "#!/usr/bin/env bash\nexit 0\n")
            environment = os.environ.copy()
            environment.update(
                {
                    "PATH": f"{temporary_path}:{environment['PATH']}",
                    "DEMO_TEST_DOCKER_LOG": str(log),
                    "LOOM_DEMO_API_HOST": "::1",
                    "LOOM_DEMO_UI_HOST": "::1",
                }
            )

            result = subprocess.run(
                [str(ROOT / "scripts/demo-up.sh")],
                cwd=ROOT,
                env=environment,
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("http://[::1]:3080", result.stdout)

    def test_demo_smoke_passes_the_expected_contract_to_the_verifier(self):
        with tempfile.TemporaryDirectory() as temporary:
            temporary_path = pathlib.Path(temporary)
            log = temporary_path / "docker.log"
            self._write_executable(
                temporary_path / "docker",
                """
                #!/usr/bin/env bash
                printf '%s\n' "$*" >> "$DEMO_TEST_DOCKER_LOG"
                """,
            )
            self._write_executable(
                temporary_path / "curl",
                """
                #!/usr/bin/env bash
                printf '<!doctype html>'
                """,
            )
            environment = os.environ.copy()
            environment.update(
                {
                    "PATH": f"{temporary_path}:{environment['PATH']}",
                    "DEMO_TEST_DOCKER_LOG": str(log),
                    "LOOM_DEMO_COMPOSE_PROJECT": "alternate-demo",
                    "LOOM_DEMO_PROJECT": "EXAMPLE_PROJECT",
                    "LOOM_DEMO_GENERATION": "example-generation",
                    "LOOM_DEMO_MANAGEMENT": "REPOSITORY",
                    "LOOM_DEMO_OUTPUT_ID": "example_output",
                    "LOOM_DEMO_OUTPUT_TITLE": "Example output",
                }
            )

            result = subprocess.run(
                [str(ROOT / "scripts/demo-smoke.sh")],
                cwd=ROOT,
                env=environment,
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            command = log.read_text().strip()
            self.assertIn("compose --project-name alternate-demo exec -T loom-api /app/loom-acceptance --smoke-only", command)
            self.assertIn("--project EXAMPLE_PROJECT", command)
            self.assertIn("--smoke-management REPOSITORY", command)
            self.assertIn("--generation example-generation", command)
            self.assertIn("--smoke-output-id example_output", command)
            self.assertIn("--smoke-output-title Example output", command)
            self.assertIn("--oracle /app/testdata/oracle.json", command)

    @staticmethod
    def _write_executable(path, content):
        path.write_text(textwrap.dedent(content).lstrip())
        path.chmod(0o755)


if __name__ == "__main__":
    unittest.main()
