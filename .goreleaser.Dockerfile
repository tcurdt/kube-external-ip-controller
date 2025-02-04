FROM alpine:3 AS builder

RUN apk update && apk upgrade && apk add --no-cache ca-certificates
WORKDIR /etc
# RUN echo "nobody:x:65534:65534:Nobody:/:" > /etc/passwd

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY ./kube-external-ip-controller /usr/local/bin/external-ip-controller
# USER nobody
WORKDIR /
CMD ["/usr/local/bin/external-ip-controller"]
