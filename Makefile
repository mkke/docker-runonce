BUILD_DATE=$(shell date "+%F_%T_%Z")
BUILD_ID=$(shell git rev-parse --short=8 HEAD)
VERPKG=main
LDFLAGS=-s -w -X $(VERPKG).buildDate=$(BUILD_DATE) -X $(VERPKG).buildID=$(BUILD_ID)

all: docker-runonce-darwin-amd64

docker-runonce-darwin-amd64: *.go ../go-prefixwriter
	CGO_ENABLED=0 GO111MODULE=on GOOS=darwin GOARCH=amd64 go build -o $@ -ldflags="$(LDFLAGS)"

docker-runonce-linux-amd64: *.go ../go-prefixwriter
	CGO_ENABLED=0 GO111MODULE=on GOOS=linux GOARCH=amd64 go build -o $@ -ldflags="$(LDFLAGS)"

clean:
	rm -f docker-runonce-darwin-amd64 docker-runonce-linux-amd64

