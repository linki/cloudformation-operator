# builder image
FROM golang:1.13-alpine3.10 as builder

ENV CGO_ENABLED 0
ENV GO111MODULE on
RUN apk --no-cache add git
WORKDIR /go/src/github.com/linki/cloudformation-operator
COPY . .
RUN go build -o /bin/cloudformation-operator -v \
  -ldflags "-X main.version=$(git describe --tags --always --dirty) -w -s" \
  ./cmd/manager

# final image
FROM alpine:3.12.0
MAINTAINER Linki <linki+docker.com@posteo.de>

RUN apk --no-cache add ca-certificates
RUN addgroup -S app && adduser -S -g app app
COPY --from=builder /bin/cloudformation-operator /bin/cloudformation-operator

USER app
ENTRYPOINT ["cloudformation-operator"]
