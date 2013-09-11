
TOP := $(shell pwd)

all:
	GOPATH=$(TOP) go install salter

deps:
	GOPATH=$(TOP) go get code.google.com/p/go.crypto

clean:
	@rm -rf pkg bin

