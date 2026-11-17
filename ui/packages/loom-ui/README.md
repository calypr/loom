# `@calypr/loom-ui`

Import Mantine's base stylesheet once in the host application, then import Loom's scoped styles:

```ts
import '@mantine/core/styles.css';
import '@calypr/loom-ui/styles.css';
```

Use `@calypr/loom-ui/viewer` for the published Explorer and `@calypr/loom-ui/builder` for authoring. The root export remains available for compatibility.

Saved-view editing, schemas, client operations, and default view/table selection
are exported from `@calypr/loom-ui/saved-views`. Pass an authenticated Loom client
to `LoomSavedViewBuilder`; use its `onSaved` callback to refresh host navigation
and Viewer queries. The host owns routes, login/CSRF handling, project context,
and page layout. Import `@calypr/loom-ui/styles.css` on the saved-view route too.
