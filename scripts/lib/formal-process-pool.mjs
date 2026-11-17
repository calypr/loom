import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { readFileSync, readdirSync } from 'node:fs';

function readProcessTable() {
    const processes = new Map();
    for (const name of readdirSync('/proc')) {
        if (!/^\d+$/.test(name)) continue;
        try {
            const stat = readFileSync(`/proc/${name}/stat`, 'utf8');
            const fields = stat.slice(stat.lastIndexOf(')') + 2).split(' ');
            processes.set(+name, { parent: +fields[1], group: +fields[2] });
        } catch (error) { if (!['ENOENT', 'ESRCH', 'EACCES'].includes(error.code)) throw error; }
    }
    return processes;
}
function descendantsOf(pid, processes = readProcessTable()) {
    const descendants = new Set([pid]);
    for (let size = -1; size !== descendants.size;) {
        size = descendants.size;
        for (const [child, value] of processes) if (descendants.has(value.parent)) descendants.add(child);
    }
    return descendants;
}
function processGroupsBelow(pid) {
    const processes = readProcessTable();
    const descendants = descendantsOf(pid, processes);
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
    const results = new Array(jobs.length), active = new Set(), cleanups = [];
    let next = 0, failure;
    const killAsync = async entry => {
        if (!entry.child.pid) return;
        const blocked = [];
        let groups = [entry.child.pid];
        try { groups = processGroupsBelow(entry.child.pid); }
        catch (error) { blocked.push(`group-enumeration: ${error.message}`); }
        for (const group of groups) {
            try { process.kill(-group, 'SIGKILL'); }
            catch (error) {
                if (error.code === 'ESRCH') continue;
                if (error.code === 'EPERM') { blocked.push(`group ${group}`); continue; }
                throw error;
            }
        }
        if (blocked.length) for (const pid of descendantsOf(entry.child.pid)) {
            try { process.kill(pid, 'SIGKILL'); }
            catch (error) { if (error.code === 'ESRCH') continue; if (error.code === 'EPERM') { blocked.push(`pid ${pid}`); continue; } throw error; }
        }
        const deadline = Date.now() + 2000;
        let live = [];
        for (;;) {
            const table = readProcessTable();
            live = [...descendantsOf(entry.child.pid, table)].filter(pid => table.has(pid));
            if (!live.length || Date.now() >= deadline) break;
            await new Promise(settle => setTimeout(settle, 25));
        }
        if (blocked.length) (failure.cleanupErrors ??= []).push(Object.assign(new Error(`cleanup: signals blocked for job ${entry.child.pid}: ${blocked.join(', ')}`), { code: 'PROC_SIGNAL_BLOCKED', blocked, survived: live }));
        if (live.length) throw Object.assign(new Error(`cleanup incomplete: job pid ${entry.child.pid} still has live descendants ${live.join(', ')}${blocked.length ? `; blocked signals: ${blocked.join(', ')}` : ''}`), { code: 'PROC_CLEANUP', pids: live, blocked });
    };
    const stop = error => {
        if (failure) return;
        failure = error;
        for (const { child } of active) {
            if (!child.pid) continue;
            try { process.kill(-child.pid, 'SIGSTOP'); }
            catch (cleanup) { if (cleanup.code !== 'ESRCH') (failure.cleanupErrors ??= []).push(cleanup); }
        }
        for (const entry of active) cleanups.push(killAsync(entry).catch(cleanup => { (failure.cleanupErrors ??= []).push(cleanup); }));
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
    await Promise.all(cleanups);
    if (failure) throw failure;
    return results;
}
