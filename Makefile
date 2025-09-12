# GITOPIA_ENV can be set to: prod, testing
# Default is prod, can be overridden via environment variable or command line
GITOPIA_ENV ?= prod

build_tags = netgo

ifeq ($(LINK_STATICALLY),true)
  ldflags += -linkmode=external -extldflags "-Wl,-z,muldefs -static"
endif
ifeq (,$(findstring nostrip,$(GIT_SERVER_BUILD_OPTIONS)))
  ldflags += -w -s
endif

build_tags += $(BUILD_TAGS)
build_tags := $(strip $(build_tags))
ldflags := $(strip $(ldflags))
BUILD_FLAGS := -tags "$(build_tags) $(GITOPIA_ENV)" -ldflags '$(ldflags)'

# check for nostrip option
ifeq (,$(findstring nostrip,$(GIT_SERVER_BUILD_OPTIONS)))
  BUILD_FLAGS += -trimpath
endif

appname := gitopia-storaged
version := $(shell echo $(shell git describe --tags) | sed 's/^v//')

build = GOOS=$(1) GOARCH=$(2) go build $(BUILD_FLAGS) -o build/gitopia-storaged$(3) ./cmd/gitopia-storaged && \
    GOOS=$(1) GOARCH=$(2) go build $(BUILD_FLAGS) -o build/gitopia-pre-receive$(3) ./hooks/gitopia-pre-receive && \
	GOOS=$(1) GOARCH=$(2) go build $(BUILD_FLAGS) -o build/gitopia-post-receive$(3) ./hooks/gitopia-post-receive
tar = cd build && tar -cvzf $(appname)_$(version)_$(1)_$(2).tar.gz gitopia-storaged$(3) gitopia-pre-receive$(3) gitopia-post-receive$(3) && \
    rm gitopia-storaged$(3) && rm gitopia-pre-receive$(3) && rm gitopia-post-receive$(3)
zip = cd build && zip $(appname)_$(version)_$(1)_$(2).zip gitopia-storaged$(3) gitopia-pre-receive$(3) gitopia-post-receive$(3) && \
    rm gitopia-storaged$(3) && rm gitopia-pre-receive$(3) && rm gitopia-post-receive$(3)

.PHONY: build

all: darwin linux git_release_tar_gz git_release_zip

clean:
	rm -rf build/

build:
		@go build $(BUILD_FLAGS) -o build/ ./cmd/gitopia-storaged
		@go build $(BUILD_FLAGS) -o build/ ./hooks/gitopia-pre-receive 
		@go build $(BUILD_FLAGS) -o build/ ./hooks/gitopia-post-receive 

##### LINUX BUILDS #####
linux: build/$(appname)_$(version)_linux_arm.tar.gz build/$(appname)_$(version)_linux_arm64.tar.gz build/$(appname)_$(version)_linux_386.tar.gz build/$(appname)_$(version)_linux_amd64.tar.gz

build/$(appname)_$(version)_linux_386.tar.gz:
	$(call build,linux,386,)
	$(call tar,linux,386)

build/$(appname)_$(version)_linux_amd64.tar.gz:
	$(call build,linux,amd64,)
	$(call tar,linux,amd64)

build/$(appname)_$(version)_linux_arm.tar.gz:
	$(call build,linux,arm,)
	$(call tar,linux,arm)

build/$(appname)_$(version)_linux_arm64.tar.gz:
	$(call build,linux,arm64,)
	$(call tar,linux,arm64)

##### DARWIN (MAC) BUILDS #####
darwin: build/$(appname)_$(version)_darwin_amd64.tar.gz build/$(appname)_$(version)_darwin_arm64.tar.gz

build/$(appname)_$(version)_darwin_arm64.tar.gz:
	$(call build,darwin,arm64,)
	$(call tar,darwin,arm64)

build/$(appname)_$(version)_darwin_amd64.tar.gz:
	$(call build,darwin,amd64,)
	$(call tar,darwin,amd64)

##### WINDOWS BUILDS #####
windows: build/$(appname)_$(version)_windows_386.zip build/$(appname)_$(version)_windows_amd64.zip

build/$(appname)_$(version)_windows_386.zip:
	$(call build,windows,386,.exe)
	$(call zip,windows,386,.exe)

build/$(appname)_$(version)_windows_amd64.zip:
	$(call build,windows,amd64,.exe)
	$(call zip,windows,amd64,.exe)

git_release_tar_gz:
	git archive --format=tar --prefix=$(appname)-$(version)/ v$(version) \
		| gzip > build/$(appname)-$(version).tar.gz

git_release_zip:
	git archive --format zip --output build/$(appname)-$(version).zip v$(version)

install: go.sum
		@echo "--> Installing gitopia services"
		@go install $(BUILD_FLAGS) -mod=readonly ./cmd/gitopia-storaged
		@go install $(BUILD_FLAGS) ./hooks/gitopia-pre-receive 
		@go install $(BUILD_FLAGS) ./hooks/gitopia-post-receive 
		
go.sum: go.mod
		@echo "--> Ensure dependencies have not been modified"
		GO111MODULE=on go mod verify

docker-build-gitopia-storage:
	@docker build -t gitopia/gitopia-storage -f Dockerfile .
