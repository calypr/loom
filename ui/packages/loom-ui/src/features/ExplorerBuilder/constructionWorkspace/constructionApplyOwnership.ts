/** Identity read at the synchronous Apply ownership boundary. */
export type ConstructionApplyIdentity<Owner> = {
  readonly owner: Owner;
  readonly requestKey: string;
  readonly generation: number;
};
export type ConstructionApplyAttempt<Owner> = { readonly identity: ConstructionApplyIdentity<Owner> };
export type ConstructionApplySlot<Owner> = { current: ConstructionApplyAttempt<Owner> | undefined };

/** Each returned completion can release/settle only the attempt that created it. */
export const beginConstructionApply = <Owner>(
  slot: ConstructionApplySlot<Owner>,
  identity: ConstructionApplyIdentity<Owner>,
  current: () => ConstructionApplyIdentity<Owner> | undefined,
  settle: (applied: boolean) => void,
): ((applied: boolean) => boolean) | undefined => {
  if (slot.current) return undefined;
  const attempt = { identity };
  slot.current = attempt;
  return (applied: boolean): boolean => {
    if (slot.current !== attempt) return false;
    slot.current = undefined;
    const live = current();
    if (!live || live.owner !== identity.owner ||
      live.requestKey !== identity.requestKey || live.generation !== identity.generation) return false;
    settle(applied);
    return true;
  };
};
