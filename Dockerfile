FROM golang:1.8 as build

WORKDIR /go/src/github.com/nlnwa/veidemann-recorderproxy
COPY . .

RUN go get -d -v ./...
RUN go install -v ./...

# Now copy it into our base image.
FROM gcr.io/distroless/base
COPY --from=build /go/bin/veidemann-recorderproxy /
EXPOSE 8080
ENV HTTP_PROXY=localhost:9999 \
    PORT=9900 \
    PROXY_COUNT=10 \
    DNS_RESOLVER=localhost:7777 \
    CONTENT_WRITER=localhost:7778 \
    BROWSER_CONTROLLER=localhost:7779 \
    CA=ca-certificates/cache-selfsignedCA.crt \
    CA_KEY=ca-certificates/cache-selfsigned.key \

CMD ["/veidemann-recorderproxy"]