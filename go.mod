module github.com/gitopia/git-server

go 1.16

require (
	github.com/buger/jsonparser v1.1.1
	github.com/cosmos/cosmos-sdk v0.45.1
	github.com/gitopia/gitopia v0.12.1-0.20220328102124-d628aacd2340
	github.com/gitopia/gitopia-ipfs-bridge v0.0.0-20220324075926-93f790f71054
	github.com/gitopia/go-git/v5 v5.4.3-0.20211224112515-b2efd9bec92c
	github.com/go-git/go-billy/v5 v5.3.1
	github.com/libgit2/git2go/v33 v33.0.4
	github.com/pkg/errors v0.9.1
	github.com/rs/cors v1.8.2
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/cobra v1.4.0
	github.com/spf13/viper v1.10.1
	github.com/tendermint/starport v0.19.3
	google.golang.org/grpc v1.45.0
)

replace github.com/gogo/protobuf => github.com/regen-network/protobuf v1.3.3-alpha.regen.1

replace github.com/tendermint/starport => github.com/gitopia/starport v0.19.5-d

replace github.com/keybase/go-keychain => github.com/99designs/go-keychain v0.0.0-20191008050251-8e49817e8af4
