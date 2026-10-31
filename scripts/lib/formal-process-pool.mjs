import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { readFileSync, readdirSync } from 'node:fs';

function processGroupsBelow(pid) {
    const processes = new Map();
    for (const name of readdirSync('/proc')) {
        if (!/^\d+$/.test(name)) continue;
        try {
            const stat = readFileSync(`/proc/${name}/stat`, 'utf8');
            const fields = stat.slice(stat.lastIndexOf(')') + 2).split(' ');
            processes.set(+name, { parent: +fields[1], group: +fields[2] });
        } catch (error) { if (!['ENOENT', 'ESRCH', 'EACCES'].includes(error.code)) throw error; }
    }
    const descendants = new Set([pid]);
    for (let size = -1; size !== descendants.size;) {
        size = descendants.size;
        for (const [child, value] of processes) if (descendants.has(value.parent)) descendants.add(child);
    }
    const ownGroup = processes.get(process.pid)?.group;
    const groups = [...new Set([...descendants].map(child => processes.get(child)?.group).filter(group => group > 0 && group !== ownGroup && group !== pid))];
    return [...groups.reverse(), pid];
}


export async function runProcessPool(jobs, { concurrency = 2, timeoutMs = 30000, maxBuffer = 8 * 1024 * 1024, cwd, env } = {}) {
    assert.ok(Array.isArray(jobs), 'Jobs must be an array');
    assert.ok(Number.isSafeInteger(concurrency) && concurrency > 0, 'Invalid concurrency');
    assert.ok(Number.isFinite(timeoutMs) && timeoutMs > 0, 'Invalid timeout');
    assert.ok(Number.isSafeInteger(maxBuffer) && maxBuffer > 0, 'Invalid output limit');
    const ids = new Set();
    for (const job of jobs) {
        assert.ok(typeof job.id === 'string' && job.id && !ids.has(job.id), 'Missing or duplicate job ID');
        ids.add(job.id);
        assert.ok(typeof job.executable === 'string' && job.executable, 'Missing executable');
        assert.ok(Array.isArray(job.args) && job.args.every(value => typeof value === 'string'), 'Invalid arguments');
    }
    const results = new Array(jobs.length), active = new Set();
    let next = 0, failure;
    const kill = entry => {
        if (!entry.child.pid) return;
        let groups = [entry.child.pid];
        try { groups = processGroupsBelow(entry.child.pid); }
        catch (error) { (failure.cleanupErrors ??= []).push(error); }
        for (const group of groups) {
            try { process.kill(-group, 'SIGKILL'); }
            catch (error) { if (error.code !== 'ESRCH') throw error; }
        }
    };
    const stop = error => {
        if (failure) return;
        failure = error;
        for (const { child } of active) {
            if (!child.pid) continue;
            try { process.kill(-child.pid, 'SIGSTOP'); }
            catch (cleanup) { if (cleanup.code !== 'ESRCH') (failure.cleanupErrors ??= []).push(cleanup); }
        }
        for (const entry of active) {
            try { kill(entry); }
            catch (cleanup) { (failure.cleanupErrors ??= []).push(cleanup); }
        }
    };
    await new Promise(resolve => {
        const schedule = () => {
            if ((failure || next === jobs.length) && active.size === 0) { resolve(); return; }
            while (!failure && active.size < concurrency && next < jobs.length) {
                const index = next++, job = jobs[index];
                let child;
                try { child = spawn(job.executable, job.args, { cwd, env: { ...process.env, ...env }, detached: true, stdio: ['ignore', 'pipe', 'pipe'] }); }
                catch (error) { error.jobId = job.id; stop(error); break; }
                const entry = { child }, stdout = [], stderr = [];
                active.add(entry);
                let bytes = 0;
                const errorFor = (message, code) => Object.assign(new Error(`${job.id}: ${message}`), { code, jobId: job.id });
                const timer = setTimeout(() => stop(errorFor(`exceeded ${timeoutMs}ms`, 'ETIMEDOUT')), timeoutMs);
                const capture = chunks => data => {
                    if (bytes + data.length > maxBuffer) { stop(errorFor(`output exceeded ${maxBuffer} bytes`, 'ENOBUFS')); return; }
                    bytes += data.length; chunks.push(data);
                };
                child.stdout.on('data', capture(stdout));
                child.stderr.on('data', capture(stderr));
                child.once('error', error => { error.jobId = job.id; stop(error); });
                child.once('exit', (status, signal) => {
                    if (status !== 0 || signal) stop(Object.assign(errorFor(`exit ${status}${signal ? ` (${signal})` : ''}`, 'PROCESS_EXIT'), { status, signal }));
                });
                child.once('close', (status, signal) => {
                    clearTimeout(timer);
                    const result = { id: job.id, pid: child.pid, status, signal, stdout: Buffer.concat(stdout).toString(), stderr: Buffer.concat(stderr).toString() };
                    results[index] = result;
                    if (failure?.jobId === job.id) Object.assign(failure, result);
                    active.delete(entry); schedule();
                });
            }
            if (failure && active.size === 0) resolve();
        };
        schedule();
    });
    if (failure) throw failure;
    return results;
}
