
TOP := $(shell pwd)

all:
	GOPATH=$(TOP) go install salter

clean:
	@rm -rf pkg bin

