import assert from 'node:assert/strict';
import { test } from 'node:test';
import { setTimeout } from 'node:timers/promises';
import { runJobs } from './batch-scheduler.mjs';

test('bounded workers process every job exactly once and retain input order', async () => {
  const jobs = Array.from({ length: 24 }, (_, id) => id);
  const seen = [], busy = new Set();
  let peak = 0;
  const result = await runJobs(jobs, ['a', 'b', 'c'], async (job, worker) => {
    assert.ok(!busy.has(worker), 'A worker cannot own overlapping jobs');
    busy.add(worker); peak = Math.max(peak, busy.size); seen.push(job);
    await setTimeout(job % 3);
    busy.delete(worker);
    return job * 2;
  });
  assert.equal(peak, 3);
  assert.deepEqual(result, jobs.map(job => job * 2));
  assert.deepEqual(seen.sort((a, b) => a - b), jobs);
});

test('empty work launches nothing and missing workers fail before execution', async () => {
  assert.deepEqual(await runJobs([], ['a'], () => assert.fail()), []);
  await assert.rejects(runJobs([1], [], () => assert.fail()), /at least one worker/);
});

test('an unexpected worker failure drains other active workers before returning', async () => {
  let finished = false;
  await assert.rejects(runJobs([0, 1], ['a', 'b'], async job => {
    if (job === 0) throw new Error('worker failed');
    await setTimeout(10); finished = true;
  }), /worker failed/);
  assert.equal(finished, true);
});
