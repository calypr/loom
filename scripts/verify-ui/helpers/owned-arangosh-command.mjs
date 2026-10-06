const shellQuote = value => `'${String(value).replaceAll("'", "'\\''")}'`;

export const buildArangoShellInvocation = ({ container, script, database = 'loom_dev' }) => ({
  command: 'rtk',
  args: [
    'proxy', 'docker', 'exec', container, 'sh', '-lc',
    `arangosh --server.endpoint tcp://127.0.0.1:8529 --server.username root --server.password "$ARANGO_ROOT_PASSWORD" --server.database ${shellQuote(database)} --javascript.execute-string ${shellQuote(script)}`,
  ],
});
