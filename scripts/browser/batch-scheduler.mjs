export async function runJobs(jobs, workers, perform) {
  if (!workers.length) throw new Error('A batch needs at least one worker');
  const results = new Array(jobs.length);
  let next = 0;
  const completed = await Promise.allSettled(workers.map(async worker => {
    while (next < jobs.length) {
      const index = next++;
      results[index] = await perform(jobs[index], worker);
    }
  }));
  const failed = completed.find(result => result.status === 'rejected');
  if (failed) throw failed.reason;
  return results;
}
