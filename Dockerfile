FROM --platform=$BUILDPLATFORM alpine:3 AS certs
RUN apk add --no-cache ca-certificates

FROM alpine:3
ARG TARGETPLATFORM
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY $TARGETPLATFORM/flagsmith /usr/local/bin/flagsmith
WORKDIR /work
ENTRYPOINT ["flagsmith"]
