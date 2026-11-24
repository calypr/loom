import { VitestTestRunner } from 'vitest/runners';

const finishTraceKey = Symbol.for('loom.ui.trace.finish-test');

export default class UiTraceRunner extends VitestTestRunner {
  onAfterTryTask(test: Parameters<VitestTestRunner['onAfterTryTask']>[0]): void {
    super.onAfterTryTask(test);
    const finish = Reflect.get(globalThis, finishTraceKey);
    if (typeof finish === 'function') finish();
  }
}
