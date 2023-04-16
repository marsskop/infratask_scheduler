FROM golang:1.18 as builder
ARG PROJECT=tfmirror
ENV GOPATH=/tmp/go
WORKDIR ${GOPATH}/src/${PROJECT}

COPY ./go.mod .
COPY ./go.sum .
COPY ./main.go .
COPY ./vendor vendor

RUN go build -v -o /infratasksch .

FROM redhat/ubi8-minimal:8.7
COPY --from=builder /infratasksch /usr/local/bin/infratasksch

USER 1000