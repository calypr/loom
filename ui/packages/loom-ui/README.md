# `@calypr/loom-ui`

Import Mantine's base stylesheet once in the host application, then import Loom's scoped styles:

```ts
import '@mantine/core/styles.css';
import '@calypr/loom-ui/styles.css';
```

Use `@calypr/loom-ui/viewer` for the published Explorer and `@calypr/loom-ui/builder` for authoring. The root export remains available for compatibility.
