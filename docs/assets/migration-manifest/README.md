# migration.yaml

`migration.example.yaml` is the reference manifest for a KCP migration. Copy it and fill in your values, then validate with:

```bash
kcp migrate validate -f migration.yaml
```

## Editor support

VS Code with the [Red Hat YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml) picks up the `# yaml-language-server: $schema=…` modeline already present in `migration.example.yaml` — autocomplete and inline validation work automatically when you copy the file.

For offline or repo-local use, map the schema in your workspace settings instead:

```jsonc
// .vscode/settings.json
"yaml.schemas": {
  "internal/manifest/migration.schema.json": ["migration*.yaml"]
}
```
