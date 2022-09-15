module github.com/gitopia/git-server

go 1.16

require (
	github.com/buger/jsonparser v1.1.1
	github.com/cosmos/cosmos-sdk v0.45.1
	github.com/gitopia/gitopia v0.13.1-0.20220914121413-ac3b172c8997
	github.com/gitopia/go-git/v5 v5.4.3-0.20211224112515-b2efd9bec92c
	github.com/go-git/go-billy/v5 v5.3.1
	github.com/kr/pretty v0.3.0 // indirect
	github.com/libgit2/git2go/v33 v33.0.4
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.12.0 // indirect
	github.com/rs/cors v1.8.2
	github.com/rs/zerolog v1.26.1 // indirect
	github.com/sirupsen/logrus v1.8.1
	github.com/spf13/afero v1.8.0 // indirect
	github.com/spf13/cobra v1.4.0
	github.com/spf13/viper v1.10.1
	github.com/tendermint/starport v0.19.3
	github.com/tendermint/tendermint v0.34.15
	golang.org/x/crypto v0.0.0-20220112180741-5e0467b6c7ce // indirect
	golang.org/x/mod v0.5.1 // indirect
	golang.org/x/net v0.0.0-20220624214902-1bab6f366d9e // indirect
	golang.org/x/sys v0.0.0-20220610221304-9f5ed59c137d // indirect
	google.golang.org/genproto v0.0.0-20220805133916-01dd62135a58 // indirect
	google.golang.org/grpc v1.48.0
	google.golang.org/protobuf v1.28.1 // indirect
	gopkg.in/ini.v1 v1.66.3 // indirect
)

replace github.com/gogo/protobuf => github.com/regen-network/protobuf v1.3.3-alpha.regen.1

replace github.com/tendermint/starport => github.com/gitopia/starport v0.19.5-d

replace github.com/keybase/go-keychain => github.com/99designs/go-keychain v0.0.0-20191008050251-8e49817e8af4
