# Build Efsn in a stock Go builder container
FROM golang:1.13-alpine as builder

RUN apk add --no-cache make gcc musl-dev linux-headers git

ADD . /efsn
RUN cd /efsn && make efsn

# Pull Efsn into a second stage deploy alpine container
FROM alpine:latest

RUN apk add --no-cache ca-certificates
COPY --from=builder /efsn/build/bin/efsn /usr/local/bin/

EXPOSE 9000 9001 9002 40408 40408/udp
ENTRYPOINT ["efsn"]
