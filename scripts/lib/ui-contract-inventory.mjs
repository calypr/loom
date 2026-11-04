import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';

export function readUiContractInventory(root) {
  const result = spawnSync('python3', [resolve(root, 'scripts/normalize_ui_contract_inventory.py'), '--expand'], {
    cwd: root,
    encoding: 'utf8',
    maxBuffer: 16 * 1024 * 1024,
  });
  if (result.error || result.status !== 0) {
    throw new Error('UI contract inventory expansion failed: ' + (result.stderr?.trim() || result.error?.message || result.status));
  }
  return JSON.parse(result.stdout);
}
