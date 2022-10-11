module github.com/gitopia/git-server

go 1.16

require (
	github.com/buger/jsonparser v1.1.1
	github.com/cosmos/cosmos-sdk v0.46.1
	github.com/gitopia/gitopia v1.0.0-rc.2
	github.com/gitopia/go-git/v5 v5.4.3-0.20221011074003-f70479dc646c
	github.com/go-git/go-billy/v5 v5.3.1
	github.com/harry-hov/go-diff v1.2.1-0.20221011071855-bbc760507cde // indirect
	github.com/ignite/cli v0.24.0
	github.com/libgit2/git2go/v33 v33.0.4
	github.com/pkg/errors v0.9.1
	github.com/rs/cors v1.8.2
	github.com/sirupsen/logrus v1.9.0
	github.com/spf13/cobra v1.5.0
	github.com/spf13/viper v1.12.0
	github.com/tendermint/tendermint v0.34.21
	google.golang.org/grpc v1.49.0
)

replace github.com/gogo/protobuf => github.com/regen-network/protobuf v1.3.3-alpha.regen.1
