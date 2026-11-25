import { useState } from 'react';
import { createRecipeEditingOwner, type RecipeEditingIdentity } from './recipeEditingOwner';

export function useRecipeEditingOwner(identity: RecipeEditingIdentity): ReturnType<ReturnType<typeof createRecipeEditingOwner>> {
  const [, rerender] = useState(0);
  const [owner] = useState(() => createRecipeEditingOwner(identity, () => rerender((value) => value + 1)));
  return owner(identity);
}
