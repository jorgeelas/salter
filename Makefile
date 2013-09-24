
TOP := $(shell pwd)

all:
	GOPATH=$(TOP) go install salter

deps:
	git submodule update --init
	GOPATH=$(TOP) go get code.google.com/p/go.crypto

clean:
	@rm -rf pkg bin

