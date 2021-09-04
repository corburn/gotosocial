# STEP ONE: build the GoToSocial binary
FROM golang:1.16.4-alpine3.13 AS binary_builder
#RUN apk update && apk upgrade --no-cache
#RUN apk add git

# create build dir
RUN mkdir -p /go/src/github.com/superseriousbusiness/gotosocial
WORKDIR /go/src/github.com/superseriousbusiness/gotosocial

# move source files
COPY cmd /go/src/github.com/superseriousbusiness/gotosocial/cmd
COPY internal /go/src/github.com/superseriousbusiness/gotosocial/internal
COPY testrig /go/src/github.com/superseriousbusiness/gotosocial/testrig
COPY docs/swagger.go /go/src/github.com/superseriousbusiness/gotosocial/docs/swagger.go

# dependencies and vendor
COPY go.mod /go/src/github.com/superseriousbusiness/gotosocial/go.mod
COPY go.sum /go/src/github.com/superseriousbusiness/gotosocial/go.sum
COPY vendor /go/src/github.com/superseriousbusiness/gotosocial/vendor

# move .git dir and version for versioning
#COPY .git /go/src/github.com/superseriousbusiness/gotosocial/.git
#COPY version /go/src/github.com/superseriousbusiness/gotosocial/version

# move the build script
COPY build.sh /go/src/github.com/superseriousbusiness/gotosocial/build.sh

# do the build step
RUN ./build.sh

# STEP TWO: build the web assets
FROM node:16.5.0-alpine3.11 AS web_builder
RUN apk update && apk upgrade --no-cache

COPY web /web
WORKDIR /web/source

RUN yarn install
RUN node build.js

# STEP THREE: bundle the admin webapp
FROM node:16.5.0-alpine3.11 AS admin_builder
RUN apk update && apk upgrade --no-cache
RUN apk add git

RUN git clone https://github.com/superseriousbusiness/gotosocial-admin
WORKDIR /gotosocial-admin

RUN npm install
RUN node index.js

# STEP FOUR: build the final container
FROM alpine:3.13 AS executor
RUN apk update && apk upgrade --no-cache

# copy over the binary from the first stage
RUN mkdir -p /gotosocial/storage
COPY --from=binary_builder /go/src/github.com/superseriousbusiness/gotosocial/gotosocial /gotosocial/gotosocial

# copy over the web directory with templates etc
COPY --from=web_builder web /gotosocial/web

# put the swagger yaml in the web assets directory so it can be accessed
COPY docs/api/swagger.yaml /gotosocial/web/assets/swagger.yaml

# copy over the admin directory
COPY --from=admin_builder /gotosocial-admin/public /gotosocial/web/assets/admin

# make the gotosocial group and user
RUN addgroup -g 1000 gotosocial
RUN adduser -HD -u 1000 -G gotosocial gotosocial

# give ownership of the gotosocial dir to the new user
RUN chown -R gotosocial gotosocial /gotosocial

# become the user
USER gotosocial

WORKDIR /gotosocial
ENTRYPOINT [ "/gotosocial/gotosocial", "server", "start" ]
