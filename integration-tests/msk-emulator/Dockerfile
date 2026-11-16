FROM public.ecr.aws/amazonlinux/amazonlinux:2023-minimal
ARG AWS_EMULATOR_VERSION
ARG ARCH
ADD https://downloads.aws.com/aws-emulator/packages/aws-emulator/LTS/aws-emulator-${AWS_EMULATOR_VERSION}-1-${ARCH}.rpm /tmp/aws-emulator.rpm
ADD https://downloads.aws.com/aws-emulator/packages/aws-emulator/LTS/aws-emulator-${AWS_EMULATOR_VERSION}-1-${ARCH}.rpm.sig /tmp/aws-emulator.rpm.sig
ADD https://downloads.aws.com/keys/gpg/current.asc /tmp/aws-publishing.asc
# gnupg2-minimal has no gpg-agent, which `gpg --import` needs, so verify against a
# dearmored keyring. Matching VALIDSIG on the primary fingerprint (not merely finding
# that fingerprint in the key file) rejects a signature from any other key the file
# might carry.
RUN gpg --batch --dearmor --output /tmp/aws-publishing.gpg /tmp/aws-publishing.asc \
    && gpg --batch --no-default-keyring --keyring /tmp/aws-publishing.gpg --status-fd 1 \
        --verify /tmp/aws-emulator.rpm.sig /tmp/aws-emulator.rpm > /tmp/gpg-status \
    && grep -q '^\[GNUPG:\] VALIDSIG .* 98BCF336A3BE53B734E967C52E5BF49BD940F624$' /tmp/gpg-status \
    && rpm -i /tmp/aws-emulator.rpm \
    && rm -rf /tmp/aws-emulator.rpm* /tmp/aws-publishing.* /tmp/gpg-status /root/.gnupg \
    && mkdir -p /home/emulator && chown 1000 /home/emulator
USER 1000
ENV HOME=/home/emulator
EXPOSE 9297
ENTRYPOINT ["aws-emulator", "-b", "0.0.0.0"]
