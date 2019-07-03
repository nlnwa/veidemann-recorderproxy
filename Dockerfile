FROM golang:1.12 as build

WORKDIR /build
COPY . .

RUN go test -v ./... && go install -v ./...

# Now copy it into our base image.
FROM gcr.io/distroless/base
COPY --from=build /go/bin/veidemann-recorderproxy /
EXPOSE 8080
ENV CACHE=localhost:9999 \
    PORT=9900 \
    PROXY_COUNT=10 \
    DNS_RESOLVER=localhost:7777 \
    CONTENT_WRITER=localhost:7778 \
    BROWSER_CONTROLLER=localhost:7779 \
    CA="" \
    CA_KEY=""

CMD ["/veidemann-recorderproxy"]