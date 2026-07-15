FROM alpine:3.22.1@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1

RUN apk add --no-cache bash iproute2 iptables
COPY --chmod=0555 wdtt-server /usr/local/bin/wdtt-server

ENTRYPOINT ["/usr/local/bin/wdtt-server"]
