
TOP := $(shell pwd)

all:
	GOPATH=$(TOP) go install github.com/dizzyd/salter

clean:
	@rm -rf pkg bin

