GITOPIA_ENV ?= prod

ifeq ($(LINK_STATICALLY),true)
  ldflags += -linkmode=external -extldflags "-Wl,-z,muldefs -static"
endif
ifeq (,$(findstring nostrip,$(GIT_SERVER_BUILD_OPTIONS)))
  ldflags += -w -s
endif
ldflags := $(strip $(ldflags))

BUILD_FLAGS := -tags "$(GITOPIA_ENV)" -ldflags '$(ldflags)'
# check for nostrip option
ifeq (,$(findstring nostrip,$(GIT_SERVER_BUILD_OPTIONS)))
  BUILD_FLAGS += -trimpath
endif

all: install

.PHONY: build

build:
		@go build $(BUILD_FLAGS) -o build/ ./cmd/git-server
		@go build $(BUILD_FLAGS) -o build/ ./hooks/gitopia-pre-receive 
		@go build $(BUILD_FLAGS) -o build/ ./hooks/gitopia-post-receive 
		@go build $(BUILD_FLAGS) -o build/ ./cmd/migrate
install: go.sum
		@echo "--> Installing gitopia services"
		@go install $(BUILD_FLAGS) -mod=readonly .
		@go install $(BUILD_FLAGS) ./hooks/gitopia-pre-receive 
		@go install $(BUILD_FLAGS) ./hooks/gitopia-post-receive 
		
go.sum: go.mod
		@echo "--> Ensure dependencies have not been modified"
		GO111MODULE=on go mod verify

docker-build-debug:
	@docker build -t gitopia/git-server -f Dockerfile .
