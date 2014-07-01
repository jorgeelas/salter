
TOP := $(shell pwd)

all: .vendor
	GOPATH=$(TOP) goop go install salter

.vendor:
	goop install

clean:
	@rm -rf pkg bin .vendor

