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

### Make it feel native (optional)

Typing the full `docker run` line every time is tedious. Wrap the plumbing in a
shell alias and then use `kcp` as if it were installed locally:

```bash
alias kcp='docker run --rm -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY -e AWS_SESSION_TOKEN -e AWS_REGION -v "$PWD:/work" -w /work --user "$(id -u):$(id -g)" confluentinc/kcp:0.9.3'

# now, for example:
kcp discover --region us-east-1
```

Because the alias is single-quoted, `$PWD` and `$(id -u)` are re-evaluated every
time you run `kcp`, so it always mounts your current directory. Add the line to
your `~/.bashrc` / `~/.zshrc` to make it permanent.

## Scanning an Apache Kafka (non-MSK) cluster

Apache Kafka sources don't use AWS, so drop the `-e AWS_*` flags and instead hand
kcp an `apache-kafka-credentials.yaml` you author yourself (schema and examples:
[Apache Kafka configuration → Credentials](apache-kafka-configuration/credentials.md)).
Keep that file in the mounted working directory so the container can read it:

```bash
docker run --rm -v "$PWD:/work" -w /work --user "$(id -u):$(id -g)" \
  confluentinc/kcp:latest scan clusters \
    --source-type apache-kafka \
    --state-file kcp-state.json \
    --credentials-file apache-kafka-credentials.yaml
```

Two container-specific gotchas:

- **The container must be able to reach your brokers.** The `bootstrap_servers`
  in the credentials file must be routable *from inside the container*. If the
  brokers run on the Docker host, use the host's real hostname/IP (on Docker
  Desktop, `host.docker.internal`); on Linux you can add `--network host`.
- **TLS/mTLS files must be mounted, with in-container paths.** If the credentials
  file points at a truststore or client keystore, copy those into the mounted
  directory and reference them by their path *inside* the container (e.g.
  `/work/truststore.jks`) — a host path like `/Users/you/...` won't exist in the
  container.

## The web UI (`kcp ui`)

`kcp ui` binds to `localhost` by default, which a container can't expose to the
host. Pass `--host 0.0.0.0` so the server listens on the container's network
interface, and publish the port on your host's **loopback** so it stays off your
network:

```bash
docker run --rm -p 127.0.0.1:5556:5556 -v "$PWD:/work" -w /work --user "$(id -u):$(id -g)" \
  confluentinc/kcp:latest ui --host 0.0.0.0 --state-file kcp-state.json
# then open http://localhost:5556
```

> **Security note:** the UI has **no authentication**. `--host 0.0.0.0` makes it
> reachable from every interface *inside* the container; the
> `-p 127.0.0.1:5556:5556` mapping is what keeps it reachable only from your own
> machine. Publishing without that loopback restriction (plain `-p 5556:5556`)
> would expose the UI to your whole network. kcp prints a warning whenever it
> binds a non-local host.

## Mirroring into JFrog Artifactory (locked-down networks)

If your network cannot reach Docker Hub directly, mirror the image through an
Artifactory **remote Docker repository** pointed at `registry-1.docker.io`,
then pull from your internal virtual repo:

```bash
docker pull your-artifactory.example.com/docker-remote/confluentinc/kcp:0.9.3
```

## Verifying provenance (optional)

Published images are signed with [cosign](https://docs.sigstore.dev/) (keyless)
by the kcp release pipeline, which runs in Confluent's Semaphore CI. Before
trusting an image you can verify it carries a signature from that pipeline:

```bash
cosign verify confluentinc/kcp:0.9.3 \
  --certificate-identity <SIGNING_IDENTITY> \
  --certificate-oidc-issuer <OIDC_ISSUER>
```

Pin `<SIGNING_IDENTITY>` and `<OIDC_ISSUER>` to the exact values the first
signed release produces. The pipeline authenticates through GCP
workload-identity federation, so the expected values are the issuer
`https://accounts.google.com` and the identity
`semaphore-kcp-release@kcp-prod.iam.gserviceaccount.com` — confirm both against
the first published signature before relying on them.

Never use a `.*`/wildcard identity or issuer pattern: a wildcard accepts a
signature from any signer and verifies nothing.
