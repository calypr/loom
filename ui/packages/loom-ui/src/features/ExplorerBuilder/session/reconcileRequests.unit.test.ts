import { expect, it, vi } from 'vitest';
import { createReconcileRequests } from './reconcileRequests';

const transport = () => ({ unwrap: async () => 'result', abort: vi.fn() });

it('supersedes work in the same slot and leaves the other slot active', () => {
  const { compileRequests, previewRequests } = createReconcileRequests();
  const firstCompile = transport();
  const preview = transport();
  const nextCompile = transport();

  const first = compileRequests.track(compileRequests.advance(), () => firstCompile);
  const activePreview = previewRequests.track(previewRequests.advance(), () => preview);
  const next = compileRequests.track(compileRequests.advance(), () => {
    expect(firstCompile.abort).toHaveBeenCalledOnce();
    return nextCompile;
  });
  first?.finish();
  first?.abort();

  expect(firstCompile.abort).toHaveBeenCalledOnce();
  expect(preview.abort).not.toHaveBeenCalled();
  expect(nextCompile.abort).not.toHaveBeenCalled();

  next?.finish();
  compileRequests.invalidate();
  expect(nextCompile.abort).not.toHaveBeenCalled();
  activePreview?.finish();
});

it('invalidates both generations before abort callbacks run', () => {
  const { compileRequests, previewRequests, invalidateCompileAndPreview } = createReconcileRequests();
  const compileGeneration = compileRequests.advance();
  const previewGeneration = previewRequests.advance();
  const startStalePreview = vi.fn(transport);
  const freshPreview = transport();

  compileRequests.track(compileGeneration, () => ({
    ...transport(),
    abort: () => {
      previewRequests.track(previewGeneration, startStalePreview);
      previewRequests.track(previewRequests.advance(), () => freshPreview);
    },
  }));
  const activePreview = transport();
  previewRequests.track(previewGeneration, () => activePreview);

  invalidateCompileAndPreview();
  expect(startStalePreview).not.toHaveBeenCalled();
  expect(activePreview.abort).toHaveBeenCalledOnce();
  expect(freshPreview.abort).not.toHaveBeenCalled();
});
