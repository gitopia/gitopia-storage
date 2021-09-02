all: install

.PHONY: build

build:
		@go build -o build/ .

install: go.sum
		@echo "--> Installing gitopia services"
		@go install -mod=readonly .

go.sum: go.mod
		@echo "--> Ensure dependencies have not been modified"
		GO111MODULE=on go mod verify
