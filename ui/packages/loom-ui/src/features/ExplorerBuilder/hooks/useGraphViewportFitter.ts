import { useEffect } from 'react';
import { useNodesInitialized, useReactFlow } from '@xyflow/react';

export interface GraphViewportBounds {
  readonly x: number;
  readonly y: number;
  readonly width: number;
  readonly height: number;
}

export const useGraphViewportFitter = ({
  expanded,
  graphIdentity,
  hostRef,
  nodeCount,
  fitBounds,
}: {
  readonly expanded: boolean;
  readonly graphIdentity: string;
  readonly hostRef: React.RefObject<HTMLDivElement | null>;
  readonly nodeCount: number;
  readonly fitBounds?: GraphViewportBounds;
}): void => {
  const flow = useReactFlow();
  const nodesInitialized = useNodesInitialized();
  const viewportInitialized = flow.viewportInitialized;
  const boundsX = fitBounds?.x;
  const boundsY = fitBounds?.y;
  const boundsWidth = fitBounds?.width;
  const boundsHeight = fitBounds?.height;

  useEffect(() => {
    const host = hostRef.current;
    if (!host || !nodesInitialized || !viewportInitialized || nodeCount === 0)
      return undefined;

    let firstFrame = 0;
    let secondFrame = 0;
    const fit = () => {
      window.cancelAnimationFrame(firstFrame);
      window.cancelAnimationFrame(secondFrame);
      firstFrame = window.requestAnimationFrame(() => {
        secondFrame = window.requestAnimationFrame(() => {
          if (boundsX !== undefined && boundsY !== undefined && boundsWidth !== undefined && boundsHeight !== undefined) {
            void flow.fitBounds(
              { x: boundsX, y: boundsY, width: boundsWidth, height: boundsHeight },
              { padding: 0.08, duration: 200 },
            );
          } else {
            void flow.fitView({
              padding: 0.08,
              minZoom: 0.05,
              maxZoom: 1.8,
              duration: 200,
            });
          }
        });
      });
    };

    const observer =
      typeof ResizeObserver === 'undefined'
        ? undefined
        : new ResizeObserver(fit);
    observer?.observe(host);
    fit();
    return () => {
      window.cancelAnimationFrame(firstFrame);
      window.cancelAnimationFrame(secondFrame);
      observer?.disconnect();
    };
  }, [
    expanded,
    flow,
    graphIdentity,
    hostRef,
    nodeCount,
    nodesInitialized,
    viewportInitialized,
    boundsX,
    boundsY,
    boundsWidth,
    boundsHeight,
  ]);
};
