FROM golang:1.12 as build

WORKDIR /build

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN go test ./... && go install -v ./...

# Now copy it into our base image.
FROM gcr.io/distroless/base
COPY --from=build /go/bin/veidemann-recorderproxy /
EXPOSE 8080
ENV CACHE_HOST=localhost \
    CACHE_PORT=9999 \
    PORT=9900 \
    PROXY_COUNT=10 \
    DNS_RESOLVER_HOST=localhost \
    DNS_RESOLVER_PORT=7777 \
    CONTENT_WRITER_HOST=localhost \
    CONTENT_WRITER_PORT=7778 \
    BROWSER_CONTROLLER_HOST=localhost \
    BROWSER_CONTROLLER_PORT=7779 \
    CA="" \
    CA_KEY=""

CMD ["/veidemann-recorderproxy"]