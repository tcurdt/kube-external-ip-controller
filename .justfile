build:
    go build -o external-ip-controller ./...

image:
    docker build -t ghcr.io/tcurdt/kube-external-ip-controller:latest .

check:
    goreleaser check
    goreleaser build --skip=validate --clean
