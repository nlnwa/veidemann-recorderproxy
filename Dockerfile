FROM golang:1.8 as build

WORKDIR /go/src/github.com/nlnwa/veidemann-recorderproxy
COPY . .

RUN go get -d -v ./...
RUN go install -v ./...

# Now copy it into our base image.
FROM gcr.io/distroless/base
COPY --from=build /go/bin/veidemann-recorderproxy /
EXPOSE 8080
ENV HTTP_PROXY=localhost:9999
CMD ["/veidemann-recorderproxy", "-v"]