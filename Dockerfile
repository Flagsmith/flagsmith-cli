ARG ALPINE_IMAGE=alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
FROM --platform=$BUILDPLATFORM $ALPINE_IMAGE AS certs
# Prepare the directories on the build platform so the target stage needs no emulation.
RUN apk add --no-cache ca-certificates && mkdir /runtime-work /runtime-home

FROM $ALPINE_IMAGE
ARG TARGETPLATFORM
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=certs --chown=65534:65534 /runtime-work/ /work/
COPY --from=certs --chown=65534:65534 /runtime-home/ /home/nobody/
COPY $TARGETPLATFORM/flagsmith /usr/local/bin/flagsmith
ENV HOME=/home/nobody
WORKDIR /work
USER 65534:65534
ENTRYPOINT ["flagsmith"]
