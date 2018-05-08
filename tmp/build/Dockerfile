FROM alpine:3.6

ADD tmp/_output/bin/cloudformation-operator /usr/local/bin/cloudformation-operator

RUN adduser -D cloudformation-operator
USER cloudformation-operator
