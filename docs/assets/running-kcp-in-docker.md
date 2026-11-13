# Run kcp in Docker

Run kcp from the `confluentinc/kcp` container image instead of installing the binary. This page covers pulling the image, giving the container AWS or Apache Kafka® credentials, running commands and the web UI, mirroring the image into a private registry, and verifying its signature.

The image is multi-architecture (`linux/amd64` and `linux/arm64`), and `docker pull` selects the right architecture, so the same commands work on Linux, macOS (Intel and Apple silicon), and Windows (through Docker Desktop or WSL 2). Windows has no separate image: Windows hosts run the Linux image through their Linux backend.

For the scanning workflow itself, including credentials, `scan clusters`, and the optional Schema Registry and connector scans, see [Get started with kcp](getting-started.md).

## Pull the image

Pull the latest image, or a pinned tag:

```bash
docker pull confluentinc/kcp:latest        # or a pinned tag, e.g. :0.9.3
```

## Prepare AWS credentials

Commands for Amazon MSK, such as `discover` and `scan clusters --source-type msk`, call AWS, so the container needs AWS credentials. A container can't reach your shell environment, your SSO session, or a helper such as `granted`. It can read only a credentials file that you mount into it, so give the container a credentials file.

**Static keys.** If you already have static keys, from `aws configure` or from a helper that writes `~/.aws/credentials`, you're ready. Skip to [Run a command](#run-a-command) and mount the file as shown there.

**AWS SSO, `granted`, or `assume`.** If you use one of these, your credentials live in your shell or are created on demand, so you have no static file to mount yet. On the host, write your active credentials into a static profile once:

```bash
# Resolve your SSO/assume profile's current creds (this runs your helper, e.g. granted):
eval "$(aws configure export-credentials --profile <your_sso_profile> --format env)"

# Write them as a static profile named "kcp" into ~/.aws/credentials:
aws configure set aws_access_key_id     "$AWS_ACCESS_KEY_ID"     --profile kcp
aws configure set aws_secret_access_key "$AWS_SECRET_ACCESS_KEY" --profile kcp
aws configure set aws_session_token     "$AWS_SESSION_TOKEN"     --profile kcp
```

This adds a `[kcp]` block to `~/.aws/credentials`:

```ini
[kcp]
aws_access_key_id = ASIA...
aws_secret_access_key = ...
aws_session_token = ...
```

!!! note
    These are temporary credentials: the key ID starts with `ASIA`, and they expire after a few hours. A mounted file is a static snapshot, so unlike kcp running natively, the container doesn't refresh them. When a command fails with an expired-token error, re-run the three `aws configure set` commands to refresh the `[kcp]` profile.

## Run a command

kcp reads and writes its state files, `kcp-state.json` and `msk-credentials.yaml`, in the working directory. In a container, bind-mount a working directory, then mount the AWS credentials file you prepared and point the AWS SDK at it:

```bash
docker run --rm \
  -v "$HOME/.aws/credentials:/aws/credentials:ro" \
  -e AWS_SHARED_CREDENTIALS_FILE=/aws/credentials -e AWS_PROFILE=kcp -e AWS_REGION=us-east-1 \
  -v "$PWD:/work" -w /work \
  --user "$(id -u):$(id -g)" \
  confluentinc/kcp:latest discover --region us-east-1
```

- `-v "$HOME/.aws/credentials:/aws/credentials:ro"` and `AWS_SHARED_CREDENTIALS_FILE` hand the container your AWS keys as a read-only file. `AWS_PROFILE=kcp` selects the profile from [Prepare AWS credentials](#prepare-aws-credentials). Mounting only the credentials file keeps the container away from `~/.aws/config` and from any `credential_process` or SSO setup, which it can't use.
- `-v "$PWD:/work" -w /work` persists the state file and lets you read the generated reports and Terraform on your host afterward.
- `--user "$(id -u):$(id -g)"` makes you the owner of the files the container writes. By default, the image runs as a non-root user with UID 65532.

Later commands reuse the mounted state file:

```bash
docker run --rm -v "$PWD:/work" -w /work --user "$(id -u):$(id -g)" \
  confluentinc/kcp:latest report plan --state-file kcp-state.json
```

### Create a shell alias (optional)

To avoid typing the full `docker run` line each time, wrap it in a shell alias, then use `kcp` as if it were installed locally:

```bash
alias kcp='docker run --rm -v "$HOME/.aws/credentials:/aws/credentials:ro" -e AWS_SHARED_CREDENTIALS_FILE=/aws/credentials -e AWS_PROFILE=kcp -e AWS_REGION=us-east-1 -v "$PWD:/work" -w /work --user "$(id -u):$(id -g)" confluentinc/kcp:0.9.3'

# now, for example:
kcp discover --region us-east-1
```

Because the alias is in single quotes, the shell evaluates `$PWD` and `$(id -u)` each time you run `kcp`, so the alias always mounts your current directory. To make the alias permanent, add the line to `~/.bashrc` or `~/.zshrc`.

## Scan an Apache Kafka cluster

Apache Kafka sources don't use AWS, so leave out the AWS credentials mount. Instead, give kcp an `apache-kafka-credentials.yaml` file that you write yourself. For the file's format and examples, see [Apache Kafka credentials](apache-kafka-configuration/credentials.md). Keep the file in the mounted working directory so the container can read it:

```bash
docker run --rm -v "$PWD:/work" -w /work --user "$(id -u):$(id -g)" \
  confluentinc/kcp:latest scan clusters \
    --source-type apache-kafka \
    --state-file kcp-state.json \
    --credentials-file apache-kafka-credentials.yaml
```

Two things are specific to running in a container:

- **The container must reach your brokers.** The `bootstrap_servers` in the credentials file must be routable from inside the container. If the brokers run on the Docker host, use the host's real hostname or IP address, or `host.docker.internal` on Docker Desktop. On Linux, you can add `--network host` instead.
- **TLS and mutual TLS (mTLS) files must be mounted, with in-container paths.** If the credentials file points at a truststore or client keystore, copy those files into the mounted directory and reference them by their path inside the container, such as `/work/truststore.jks`. A host path such as `/Users/you/...` doesn't exist in the container.

## Run the web UI

By default, `kcp ui` binds to `localhost`, which a container can't expose to the host. Pass `--host 0.0.0.0` so the server listens on the container's network interface, and publish the port to your host's loopback interface so it stays off your network:

```bash
docker run --rm --publish 127.0.0.1:5556:5556 -v "$PWD:/work" -w /work --user "$(id -u):$(id -g)" \
  confluentinc/kcp:latest ui --host 0.0.0.0 --state-file kcp-state.json
# then open http://localhost:5556
```

### Expose the UI beyond your machine

`kcp ui` has no authentication: anyone who can reach it has full access to your state file, including clusters, costs, and topology, and to the asset-generation wizard. `--host 0.0.0.0` makes the server listen on every interface, so use it only with a network boundary in place:

- **In a container.** Publish to loopback only, with `--publish 127.0.0.1:<port>:<port>`. A publish without the `127.0.0.1` prefix, such as `--publish 5556:5556`, binds every host interface and exposes the UI to your whole network.
- **On a shared host or network.** Restrict access with a firewall or security group rule scoped to trusted source IP addresses and to the specific port you chose. Choose that port deliberately so the rule can be precise.
- **The port isn't a security control.** A non-default port is obscurity, not protection. The network boundary is what protects you. kcp prints a warning whenever it binds to a non-local host.

To run on a non-default port, set the same value in the publish mapping and in `--port`:

```bash
docker run --rm --publish 127.0.0.1:8080:8080 -v "$PWD:/work" -w /work --user "$(id -u):$(id -g)" \
  confluentinc/kcp:latest ui --host 0.0.0.0 --port 8080 --state-file kcp-state.json
```

## Mirror the image into JFrog Artifactory

If your network can't reach Docker Hub directly, mirror the image through an Artifactory remote Docker repository that points at `registry-1.docker.io`, then pull from your internal virtual repository:

```bash
docker pull your-artifactory.example.com/docker-remote/confluentinc/kcp:0.9.3
```

## Verify the image signature (optional)

The kcp release pipeline, which runs in Confluent's Semaphore CI, signs published images with keyless [cosign](https://docs.sigstore.dev/). Before you trust an image, you can verify that it carries a signature from that pipeline:

```bash
cosign verify confluentinc/kcp:0.9.3 \
  --certificate-identity <signing_identity> \
  --certificate-oidc-issuer <oidc_issuer>
```

Set `<signing_identity>` and `<oidc_issuer>` to the exact values the first signed release produces. The pipeline authenticates through Google Cloud workload identity federation, so the expected values are the issuer `https://accounts.google.com` and the identity `semaphore-kcp-release@kcp-prod.iam.gserviceaccount.com`. Confirm both against the first published signature before you rely on them.

Never use a wildcard identity or issuer pattern, such as `.*`. A wildcard accepts a signature from any signer and verifies nothing.

## Related content

- [Get started with kcp](getting-started.md)
- [Migrate to Confluent Cloud with kcp](README.md)
- [Apache Kafka credentials](apache-kafka-configuration/credentials.md)
- [`kcp ui` command reference](command-reference/ui.md)
- Sigstore: [Cosign documentation](https://docs.sigstore.dev/)
