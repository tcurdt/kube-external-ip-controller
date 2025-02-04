FROM golang:1.23-alpine AS builder

RUN apk update && apk upgrade && apk add --no-cache ca-certificates
WORKDIR /app
ADD . /app
RUN echo "nobody:x:65534:65534:Nobody:/:" > /app/passwd
RUN ls -la
RUN go -o external-ip-controller build ./...

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/passwd /etc/passwd
COPY --from=builder /app/external-ip-controller /usr/local/bin/external-ip-controller
USER nobody
WORKDIR /
CMD ["/usr/local/bin/external-ip-controller"]
