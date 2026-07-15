FROM alpine:3.22.1

RUN apk add --no-cache bash iproute2 iptables
COPY --chmod=0555 wdtt-server /usr/local/bin/wdtt-server

ENTRYPOINT ["/usr/local/bin/wdtt-server"]
