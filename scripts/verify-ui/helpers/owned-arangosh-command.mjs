const shellQuote = value => `'${String(value).replaceAll("'", "'\\''")}'`;

export const buildArangoShellInvocation = ({ container, script, database = 'loom_dev' }) => ({
  command: 'rtk',
  args: [
    'proxy', 'docker', 'exec', container, 'sh', '-lc',
    `set -e; umask 077; script_file=$(mktemp /tmp/loom-arangosh.XXXXXX); trap 'rm -f "$script_file"' EXIT; printf '%s' ${shellQuote(script)} > "$script_file"; arangosh --server.endpoint tcp://127.0.0.1:8529 --server.username root --server.password "$ARANGO_ROOT_PASSWORD" --server.database ${shellQuote(database)} --javascript.execute "$script_file"`,
  ],
});
