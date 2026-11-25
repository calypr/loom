import { createTheme, Popover } from '@mantine/core';

export const anchoredPopoverTheme = createTheme({
  components: {
    Popover: Popover.extend({
      defaultProps: {
        closeOnEscape: false,
        floatingStrategy: 'fixed',
        offset: 4,
        transitionProps: { duration: 0 },
        unstyled: true,
        withinPortal: false,
        withRoles: false,
      },
    }),
  },
});
