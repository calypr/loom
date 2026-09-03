import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "acceptance-real.sh"
WORKFLOW = ROOT / ".github" / "workflows" / "acceptance.yaml"
PERFORMANCE = ROOT / "scripts" / "acceptance-performance.sh"


class AcceptanceRealScriptTest(unittest.TestCase):
    def test_shell_syntax(self) -> None:
        subprocess.run(["bash", "-n", str(SCRIPT)], cwd=ROOT, check=True)
        subprocess.run(["bash", "-n", str(PERFORMANCE)], cwd=ROOT, check=True)

    def test_local_and_github_wiring_contract(self) -> None:
        source = SCRIPT.read_text(encoding="utf-8")

        self.assertIn('start_forward "$clickhouse_service" 8123 clickhouse-http', source)
        self.assertIn('start_forward "$clickhouse_service" 9000 clickhouse-native', source)
        self.assertIn('--clickhouse-url "$clickhouse_native_url"', source)
        self.assertIn('--run-id "$run_id"', source)
        self.assertIn('server_config=$(mktemp "${TMPDIR:-/tmp}/loom-acceptance-server-config.XXXXXX.json")', source)
        self.assertIn('chmod 600 "$server_config"', source)
        self.assertIn("jq -n \\", source)
        self.assertIn('--arg schema "$server_source_root/schemas/graph-fhir.json"', source)
        self.assertIn('"$build_dir/arango-fhir-server" --config "$server_config" --no-auth', source)
        self.assertIn('LOOM_ACCEPTANCE_CLICKHOUSE_PASSWORD="$clickhouse_password" "$build_dir/loom-acceptance"', source)
        self.assertNotIn('--clickhouse-password "$clickhouse_password"', source)
        self.assertNotIn('--arg clickhouse_password "$clickhouse_password"', source)
        self.assertNotIn('curl -fsS --user "$clickhouse_username:$clickhouse_password"', source)
        self.assertIn('LOOM_ACCEPTANCE_CONFIG_CLICKHOUSE_PASSWORD="$clickhouse_password" jq -n', source)
        self.assertIn('--config "$clickhouse_curl_config"', source)
        self.assertIn("LOOM_ACCEPTANCE_KUBE_CONFIG_SECRET:-loom-config", source)
        self.assertIn("get secret \"$secret_name\" -o jsonpath='{.data.config\\.yaml}'", source)
        self.assertIn(".server.clickhouse.username", source)
        self.assertIn(".server.clickhouse.password", source)
        self.assertIn('LOOM_ACCEPTANCE_CURL_USER="$clickhouse_username:$clickhouse_password"', source)
        cleanup = source.split("cleanup() {", 1)[1].split("trap cleanup EXIT", 1)[0]
        self.assertLess(cleanup.index("clickhouse_http_request"), cleanup.index('for pid in "${processes'))
        self.assertNotIn("arango-fhir-proto", source)
        self.assertNotIn("$repo_root/.gocache", source)

        workflow = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn("http://127.0.0.1:8529/_api/version", workflow)
        self.assertNotIn("http://localhost:8529/_api/version", workflow)
        self.assertIn("\n  pull_request:\n", workflow)
        self.assertNotIn("\n  push:\n", workflow)
        self.assertIn("CLICKHOUSE_USER: loom_ci", workflow)
        self.assertIn("CLICKHOUSE_PASSWORD: loom_ci_password", workflow)
        self.assertIn("--user=loom_ci --password=loom_ci_password", workflow)
        self.assertIn("fetch-depth: 0", workflow)
        self.assertIn("run: make acceptance-performance", workflow)
        self.assertIn("- name: Summarize acceptance evidence", workflow)
        self.assertIn("GITHUB_STEP_SUMMARY", workflow)
        self.assertIn("current/report.json", workflow)
        self.assertIn("vertices_inserted", workflow)
        self.assertIn("row_digest", workflow)
        self.assertIn("cleanup_status", workflow)
        self.assertNotIn("LOOM_ACCEPTANCE_ALLOW_BASE_UNAVAILABLE", workflow)

        performance = PERFORMANCE.read_text(encoding="utf-8")
        self.assertIn('git archive "$base_ref"', performance)
        self.assertIn('LOOM_ACCEPTANCE_SERVER_SOURCE_ROOT="$source_root"', performance)
        self.assertIn('if [[ ! -f "$base_source/cmd/loom-acceptance/main.go" ]]', performance)
        self.assertIn('LOOM_ACCEPTANCE_ALLOW_BASE_UNAVAILABLE=true base_unavailable "base commit predates the acceptance protocol"', performance)
        self.assertIn("--performance-repeat-base-report", performance)
        self.assertIn("--performance-repeat-current-report", performance)


if __name__ == "__main__":
    unittest.main()
