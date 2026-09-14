import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "acceptance-real.sh"
PERFORMANCE = ROOT / "scripts" / "acceptance-performance.sh"
COMPOSE = ROOT / "compose.yaml"
WORKFLOW = ROOT / ".github" / "workflows" / "acceptance.yaml"


class AcceptanceDeploymentContractTest(unittest.TestCase):
    def test_shell_syntax(self) -> None:
        for script in (SCRIPT, PERFORMANCE, ROOT / "scripts" / "demo-up.sh"):
            subprocess.run(["bash", "-n", str(script)], cwd=ROOT, check=True)

    def test_acceptance_real_uses_compose_demo_transaction_and_smoke(self) -> None:
        source = SCRIPT.read_text(encoding="utf-8")

        self.assertIn('canonical_project=${LOOM_DEMO_COMPOSE_PROJECT:-loom-demo}', source)
        self.assertIn('run_id=${LOOM_ACCEPTANCE_RUN_ID:-$acceptance_id}', source)
        self.assertIn('LOOM_DEMO_SEED=false', source)
        self.assertIn('acceptance_project="loom-acceptance-$acceptance_id"', source)
        self.assertIn('LOOM_ACCEPTANCE_PROJECT:-NCPI_ACCEPTANCE', source)
        self.assertIn('"$repo_root/scripts/demo-up.sh"', source)
        self.assertIn('"$repo_root/scripts/demo-smoke.sh"', source)
        self.assertIn('"$repo_root/scripts/demo-browser-smoke.sh"', source)
        self.assertIn("jq -e '.status == \"PASSED\"'", source)
        self.assertIn("run --rm --no-deps -T --entrypoint /bin/sh demo-seed", source)
        self.assertIn("tar -C /var/lib/loom/artifacts -cf - .", source)
        self.assertIn('tar -C "$artifacts" -xf -', source)
        self.assertIn("compose-ps.txt", source)
        self.assertIn("compose-images.txt", source)
        self.assertNotIn("kubectl", source)
        self.assertNotIn("Kubernetes", source)
        self.assertNotIn("LOOM_ACCEPTANCE_MODE", source)
        self.assertIn('scripts/demo-down.sh" --volumes --remove-orphans', source)
        self.assertIn('deployment_project:$deployment_project', source)
        self.assertIn('acceptance_project:$acceptance_project', source)

    def test_compose_has_source_contexts_run_namespace_and_named_artifacts(self) -> None:
        source = COMPOSE.read_text(encoding="utf-8")

        self.assertIn("context: ${LOOM_API_BUILD_CONTEXT:-.}", source)
        self.assertIn("context: ${LOOM_UI_BUILD_CONTEXT:-./ui}", source)
        self.assertIn("${LOOM_DEMO_RUN_ID:-d000000000000001}", source)
        self.assertIn("image: ${LOOM_UI_IMAGE:-loom-demo-ui:local}", source)
        self.assertIn("${LOOM_DEMO_FIXTURE_CACHE_DIR:-fixture_cache}:/var/cache/loom", source)
        self.assertIn("demo_artifacts:/var/lib/loom/artifacts", source)

    def test_performance_isolates_projects_ports_images_and_cleanup(self) -> None:
        source = PERFORMANCE.read_text(encoding="utf-8")

        self.assertIn('git archive "$base_ref"', source)
        self.assertIn('project="${compose_prefix}-${comparison_id}-${name}"', source)
        self.assertIn('read -r api_port ui_port <<<"$(free_ports)"', source)
        self.assertIn('LOOM_DEMO_SOURCE_ROOT="$source_root"', source)
        self.assertIn('LOOM_API_BUILD_CONTEXT="$source_root"', source)
        self.assertIn('LOOM_UI_BUILD_CONTEXT="$source_root/ui"', source)
        self.assertIn('LOOM_API_IMAGE="loom-acceptance-api:${comparison_id}-${name}"', source)
        self.assertIn('LOOM_UI_IMAGE="loom-acceptance-ui:${comparison_id}-${name}"', source)
        self.assertIn('LOOM_DEMO_FIXTURE_CACHE_DIR="$cache"', source)
        self.assertIn('LOOM_ACCEPTANCE_FIXTURE_PREPARED=true', source)
        self.assertIn('LOOM_ACCEPTANCE_ISOLATED=true', source)
        self.assertIn('active_project=$project', source)
        self.assertIn('if [[ -n "$active_project" ]]', source)
        self.assertIn('if [[ "$name" == *-repeat ]]; then browser_smoke=false; fi', source)
        self.assertIn('scripts/demo-down.sh" --volumes --remove-orphans', source)
        self.assertIn("--performance-repeat-base-report", source)
        self.assertIn("--performance-repeat-current-report", source)

    def test_github_workflow_uses_compose_performance_path(self) -> None:
        source = WORKFLOW.read_text(encoding="utf-8")

        self.assertNotIn("services:", source)
        self.assertNotIn("LOOM_ACCEPTANCE_MODE", source)
        self.assertIn("LOOM_ACCEPTANCE_BROWSER_SMOKE: false", source)
        self.assertIn("run: make acceptance-performance", source)
        self.assertIn("fetch-depth: 0", source)
        self.assertIn("GITHUB_STEP_SUMMARY", source)
        self.assertIn("current/report.json", source)
        self.assertIn("cleanup_status", source)
        self.assertIn("actions/upload-artifact@v4", source)


if __name__ == "__main__":
    unittest.main()
