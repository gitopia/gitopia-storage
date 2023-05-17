all: install

.PHONY: build

build:
		@go build -o build/ .
		@go build -o build/ ./cmd/git-server-events
		@go build -o build/ ./hooks/gitopia-pre-receive 
		@go build -o build/ ./hooks/gitopia-post-receive 
		
install: go.sum
		@echo "--> Installing gitopia services"
		@go install -mod=readonly .
		@go install ./hooks/gitopia-pre-receive 
		@go install ./hooks/gitopia-post-receive 
		
go.sum: go.mod
		@echo "--> Ensure dependencies have not been modified"
		GO111MODULE=on go mod verify
