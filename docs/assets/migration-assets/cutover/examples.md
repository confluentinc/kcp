# Example manifests

One fully-annotated, ready-to-copy manifest per migration type — every field,
with comments. Each is also available as a plain file under
`gateway-examples/` (a link downloads the raw file rather than opening it
in-browser).

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
[Converting a dynamic route to static](route-conversion.md)).
[`gateway-route-conversion.yaml`](../../gateway-examples/gateway-route-conversion.yaml)

```yaml
--8<-- "docs/assets/gateway-examples/gateway-route-conversion.yaml"
```
