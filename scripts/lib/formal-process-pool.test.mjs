import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, existsSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { runProcessPool } from './formal-process-pool.mjs';

const job = (id, source, ...args) => ({ id, executable: process.execPath, args: ['-e', source, ...args] });
const temporary = t => { const path = mkdtempSync(join(tmpdir(), 'loom-pool-test-')); t.after(() => rmSync(path, { recursive: true, force: true })); return path; };
function running(pid) {
    try { return !/\) Z /.test(readFileSync(`/proc/${pid}/stat`, 'utf8')); }
    catch (error) { if (error.code === 'ENOENT') return false; throw error; }
}

test('real jobs overlap up to the bound and results retain input order', async t => {
    const directory = temporary(t), events = join(directory, 'events'), release = join(directory, 'release');
    const source = `const fs=require('node:fs');const[id,events,release]=process.argv.slice(1);fs.appendFileSync(events,'start '+id+'\\n');const finish=()=>{fs.appendFileSync(events,'finish '+id+'\\n');console.log(id)};if(id==='A'){const timer=setInterval(()=>{if(fs.existsSync(release)){clearInterval(timer);finish()}},5)}else if(id==='B'){const timer=setInterval(()=>{if(fs.readFileSync(events,'utf8').includes('start A')){clearInterval(timer);fs.writeFileSync(release,'ready');finish()}},5)}else{finish()}`;
    const result = await runProcessPool(['A', 'B', 'C'].map(id => job(id, source, id, events, release)), { concurrency: 2, timeoutMs: 5000 });
    assert.deepEqual(result.map(value => value.stdout.trim()), ['A', 'B', 'C']);
    let active = 0, maximum = 0;
    const lines = readFileSync(events, 'utf8').trim().split('\n');
    for (const line of lines) { active += line.startsWith('start ') ? 1 : -1; maximum = Math.max(maximum, active); assert.ok(active >= 0 && active <= 2); }
    assert.equal(maximum, 2); assert.equal(active, 0);
    assert.ok(lines.indexOf('start B') < lines.indexOf('finish A'));
});

test('first failed job cancels its sibling process group and prevents later launches', async t => {
    const directory = temporary(t), pids = join(directory, 'pids'), later = join(directory, 'later');
    const resistant = `const fs=require('node:fs'),{spawn}=require('node:child_process');process.on('SIGTERM',()=>{});const child=spawn(process.execPath,['-e','process.on("SIGTERM",()=>{});setInterval(()=>{},1000)'],{stdio:'inherit',detached:true});fs.writeFileSync(process.argv[1],JSON.stringify([process.pid,child.pid]));setInterval(()=>{},1000)`;
    const fail = `const fs=require('node:fs');const timer=setInterval(()=>{if(fs.existsSync(process.argv[1])){clearInterval(timer);console.error('deliberate failure');process.exit(7)}},5)`;
    await assert.rejects(runProcessPool([
        job('resistant', resistant, pids), job('failure', fail, pids),
        job('must-not-run', `require('node:fs').writeFileSync(process.argv[1],'launched')`, later),
    ], { concurrency: 2, timeoutMs: 5000 }), error => error.code === 'PROCESS_EXIT' && error.jobId === 'failure' && error.status === 7 && error.stderr.includes('deliberate failure'));
    assert.equal(existsSync(later), false);
    for (const pid of JSON.parse(readFileSync(pids, 'utf8'))) assert.equal(running(pid), false, `worker ${pid} remains running`);
});

test('deadline terminates a SIGTERM-resistant child before rejecting', async t => {
    const directory = temporary(t), ready = join(directory, 'ready');
    let childPid;
    await assert.rejects(runProcessPool([job('deadline', `process.on('SIGTERM',()=>{});require('node:fs').writeFileSync(process.argv[1],String(process.pid));setInterval(()=>{},1000)`, ready)], { timeoutMs: 1000 }), error => {
        childPid = error.pid;
        return error.code === 'ETIMEDOUT' && error.jobId === 'deadline' && error.signal === 'SIGKILL';
    });
    assert.equal(Number(readFileSync(ready, 'utf8')), childPid);
    assert.equal(running(childPid), false);
});

test('combined stdout and stderr are bounded and overflow cancels the producer', async () => {
    let childPid;
    await assert.rejects(runProcessPool([job('noisy', `process.stdout.write('x'.repeat(768));process.stderr.write('y'.repeat(768));setInterval(()=>{},1000)`) ], { maxBuffer: 1024 }), error => {
        childPid = error.pid;
        return error.code === 'ENOBUFS' && error.jobId === 'noisy' && Buffer.byteLength(error.stdout) + Buffer.byteLength(error.stderr) <= 1024;
    });
    assert.equal(running(childPid), false);
});

test('spawn failures retain their native code and invalid batches launch nothing', async () => {
    await assert.rejects(runProcessPool([{ id: 'missing', executable: '/no-such-loom-pool-executable', args: [] }]), error => error.code === 'ENOENT' && error.jobId === 'missing');
    await assert.rejects(runProcessPool([job('duplicate', ''), job('duplicate', '')]), /duplicate job ID/);
    assert.deepEqual(await runProcessPool([]), []);
});
