FROM quay.io/app-sre/golang:1.18.5 as builder
WORKDIR /build
COPY . .
RUN make test build

FROM registry.access.redhat.com/ubi8-minimal
COPY --from=builder /build/git-partition-sync-producer  /bin/git-partition-sync-producer
COPY query.graphql /
RUN microdnf update -y && microdnf install -y git && microdnf install -y ca-certificates

ENTRYPOINT  [ "/bin/git-partition-sync-producer" ]