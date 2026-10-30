# Example Cutover Manifests

This page has one fully annotated, ready-to-copy manifest per migration type,
with comments on every field. Each is also available as a plain file under
`gateway-examples/`. Each file link downloads the raw file rather than opening it
in the browser.

## Static migration

[`gateway-static-migration.yaml`](../../gateway-examples/gateway-static-migration.yaml)

```yaml
--8<-- "docs/assets/gateway-examples/gateway-static-migration.yaml"
```

## Dynamic migration

[`gateway-dynamic-migration.yaml`](../../gateway-examples/gateway-dynamic-migration.yaml)

```yaml
--8<-- "docs/assets/gateway-examples/gateway-dynamic-migration.yaml"
```

## Route conversion

A route conversion declares `spec.dynamicToStaticConversion` in place of a
migration section (see
[Route Conversion Settings in the Migration Manifest](route-conversion.md)).
[`gateway-route-conversion.yaml`](../../gateway-examples/gateway-route-conversion.yaml)

```yaml
--8<-- "docs/assets/gateway-examples/gateway-route-conversion.yaml"
```

## Related content

- [Cutover migration manifest reference](overview.md)
- [Run a migration cutover](running-a-cutover.md)
- [Migrate with a static Gateway route](../../technical-guides/migrations/gateway/static-migration.md)
- [Migrate with a dynamic Gateway route](../../technical-guides/migrations/gateway/dynamic-migration.md)
- [Convert a dynamic Gateway route to static](../../technical-guides/migrations/gateway/dynamic-to-static-conversion.md)
