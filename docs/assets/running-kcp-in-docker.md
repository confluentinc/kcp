# Running kcp in Docker

kcp is published as a multi-arch image (`linux/amd64` + `linux/arm64`) at
`confluentinc/kcp`. `docker pull` auto-selects the right architecture, so the
same commands work on Linux, macOS (Intel + Apple Silicon), and Windows (via
Docker Desktop / WSL2). There is no Windows-specific image — Windows hosts run
the Linux image through their Linux backend.

## Pull

```bash
docker pull confluentinc/kcp:latest        # or a pinned tag, e.g. :0.9.3
```

## Run

kcp reads and writes its state files (`kcp-state.json`, `msk-credentials.yaml`)
in the working directory, and reads AWS credentials from the standard AWS SDK
credential chain. In a container you therefore (1) bind-mount a working
directory and (2) pass AWS credentials as environment variables.

```bash
docker run --rm \
  -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY -e AWS_SESSION_TOKEN -e AWS_REGION \
  -v "$PWD:/work" -w /work \
  --user "$(id -u):$(id -g)" \
  confluentinc/kcp:latest discover --region us-east-1
```

- `-v "$PWD:/work" -w /work` persists the state file and lets you read the
  generated reports/Terraform on your host afterward.
- `--user "$(id -u):$(id -g)"` keeps files the container writes owned by you
  (the image runs as a non-root user, uid 65532, by default).

Subsequent commands reuse the mounted state file:

```bash
docker run --rm -v "$PWD:/work" -w /work --user "$(id -u):$(id -g)" \
  confluentinc/kcp:latest report plan --state-file kcp-state.json
```

## The web UI (`kcp ui`)

```bash
docker run --rm -p 5556:5556 -v "$PWD:/work" -w /work --user "$(id -u):$(id -g)" \
  confluentinc/kcp:latest ui --state-file kcp-state.json
# then open http://localhost:5556
```

> **Known limitation:** `kcp ui` currently binds to `localhost` inside the
> container rather than `0.0.0.0`, and there is no `--host`/`--bind` flag to
> change that. Because of this, the published port above is **not**
> reachable from the host — `http://localhost:5556` will not load. Until
> container-wide binding is supported, run `kcp ui` directly on the host
> (outside a container) to use the web UI.

## Mirroring into JFrog Artifactory (locked-down networks)

If your network cannot reach Docker Hub directly, mirror the image through an
Artifactory **remote Docker repository** pointed at `registry-1.docker.io`,
then pull from your internal virtual repo:

```bash
docker pull your-artifactory.example.com/docker-remote/confluentinc/kcp:0.9.3
```

## Verifying provenance (optional)

Each release publishes an SBOM for its binaries (as a release asset), and
each published image is cosign-signed (keyless, via GitHub Actions OIDC):

```bash
cosign verify confluentinc/kcp:0.9.3 \
  --certificate-identity-regexp '^https://github.com/confluentinc/kcp/\.github/workflows/release\.yml@.+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The identity must match the release workflow that actually signed the image;
never use `.*`/wildcard identity or issuer patterns, since they accept a
signature from any signer and verify nothing.
