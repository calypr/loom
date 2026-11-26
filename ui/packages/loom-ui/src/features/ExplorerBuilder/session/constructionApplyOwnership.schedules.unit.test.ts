import { describe, expect, it } from 'vitest';
import { construction_apply_admission as admissions, construction_apply_ownership as cases } from '../../../../testdata/schedules/construction_apply_ownership';
import { beginConstructionApply, type ConstructionApplyIdentity } from './constructionApplyOwnership';

type Owner = string;

describe('construction Apply ownership schedules', () => {
  it.each(cases)('$name', (schedule) => {
    const slot = { current: undefined };
    let live: ConstructionApplyIdentity<Owner> = {
      owner: schedule.owner,
      requestKey: schedule.requestKey,
      generation: schedule.generation,
    };
    const settlements: boolean[] = [];
    const first = beginConstructionApply(slot, live, () => live, (applied) => settlements.push(applied));
    if (!first) throw new Error('initial Apply was not admitted');

    if ('replacement' in schedule) {
      const old = first;
      slot.current = undefined;
      const replacementIdentity: ConstructionApplyIdentity<Owner> = {
        owner: schedule.replacement.owner,
        requestKey: schedule.replacement.requestKey,
        generation: schedule.replacement.generation,
      };
      live = replacementIdentity;
      const replacement = beginConstructionApply(slot, live, () => live, (applied) => settlements.push(applied));
      expect(old?.(true)).toBe(schedule.accepted);
      expect(slot.current).toBeDefined();
      expect(replacement?.(schedule.replacement.applied)).toBe(schedule.replacementAccepted);
    } else {
      live = schedule.current;
      expect(first?.(schedule.applied)).toBe(schedule.accepted);
      if ('duplicateCompletionRejected' in schedule) expect(first?.(schedule.applied)).toBe(false);
    }

    expect(settlements).toEqual(schedule.settlements);
    if ('replacement' in schedule) expect(slot.current).toBeUndefined();
  });

  it.each(admissions)('$name', (schedule) => {
    const current = { owner: schedule.current.owner, requestKey: schedule.current.requestKey, generation: schedule.current.generation };
    const slot = { current: { identity: current } };
    const occupied = slot.current;
    const attempt = beginConstructionApply(
      slot,
      { owner: schedule.requested.owner, requestKey: schedule.requested.requestKey, generation: schedule.requested.generation },
      () => schedule.requested,
      () => undefined,
    );
    expect(attempt !== undefined).toBe(schedule.expectedAdmitted);
    expect(slot.current).toBe(occupied);
  });
});
