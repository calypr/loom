import { useCallback, useState, useSyncExternalStore } from 'react';
import { createStore } from '../../../data/createStore';
import {
  layoutDatasetGraph,
  type GraphLayoutEdge,
  type GraphLayoutNode,
} from '../model/graphLayout';

interface GraphLayoutState {
  readonly identity?: string;
  readonly positions: ReadonlyMap<string, { x: number; y: number }>;
}

export const useGraphLayout = (
  identity: string,
  nodes: ReadonlyArray<GraphLayoutNode>,
  edges: ReadonlyArray<GraphLayoutEdge>,
): GraphLayoutState => {
  const [store] = useState(() => createStore<GraphLayoutState>({ positions: new Map() }));

  const subscribe = useCallback((onChange: () => void) => {
    const unsubscribe = store.subscribe(onChange);
    let active = true;
    store.setSnapshot({ positions: new Map() });
    void layoutDatasetGraph(nodes, edges)
      .then((value) => {
        if (active) store.setSnapshot({ identity, positions: value.positions });
      })
      .catch(() => {
        if (active) store.setSnapshot({ identity, positions: new Map() });
      });
    return () => {
      active = false;
      unsubscribe();
    };
  }, [edges, identity, nodes, store]);

  return useSyncExternalStore(subscribe, store.getSnapshot, store.getSnapshot);
};
