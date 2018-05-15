# builder image
FROM golang:1.10-alpine as builder

RUN apk --no-cache add git
RUN go get github.com/golang/dep/cmd/dep
WORKDIR /go/src/github.com/linki/cloudformation-operator
COPY . .
RUN dep ensure -vendor-only
RUN go build -o /bin/cloudformation-operator -v \
  -ldflags "-X main.version=$(git describe --tags --always --dirty) -w -s" \
  ./cmd/cloudformation-operator

# final image
FROM alpine:3.7
MAINTAINER Linki <linki+docker.com@posteo.de>

RUN apk --no-cache add ca-certificates
RUN addgroup -S app && adduser -S -g app app
COPY --from=builder /bin/cloudformation-operator /bin/cloudformation-operator

USER app
ENTRYPOINT ["cloudformation-operator"]
