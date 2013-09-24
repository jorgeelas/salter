
TOP := $(shell pwd)

all:
	GOPATH=$(TOP) go install salter

deps:
	git submodule update --init

clean:
	@rm -rf pkg bin

