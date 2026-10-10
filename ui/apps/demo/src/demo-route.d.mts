export type DemoNavigation =
  | {
      readonly kind: 'mode';
      readonly project: string;
      readonly organization?: string;
      readonly explorerId: string;
      readonly mode: 'builder' | 'viewer';
    }
  | {
      readonly kind: 'explorer';
      readonly project: string;
      readonly organization?: string;
      readonly explorerId: string;
    }
  | {
      readonly kind: 'repair';
      readonly project: string;
      readonly organization?: string;
      readonly explorerId: string;
      readonly focus: { readonly outputId: string; readonly column: string; readonly label: string };
    };

export function updateDemoNavigationURL(currentURL: URL, navigation: DemoNavigation): URL;

export function viewerProjectID(organization: string | undefined, project: string): string;
