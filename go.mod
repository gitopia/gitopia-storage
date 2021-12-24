module github.com/gitopia/git-server

go 1.16

require (
	github.com/gitopia/gitopia v0.10.0
	github.com/gitopia/go-git/v5 v5.4.3-0.20211224112515-b2efd9bec92c
	github.com/gitopia/goar v0.0.0-20210912164232-a48c38a69bc2
	github.com/go-git/go-billy/v5 v5.3.1
	github.com/libgit2/git2go/v33 v33.0.4
	github.com/rs/cors v1.8.0
	github.com/spf13/viper v1.8.1
	google.golang.org/grpc v1.40.0
)

replace google.golang.org/grpc => google.golang.org/grpc v1.33.2

replace github.com/gogo/protobuf => github.com/regen-network/protobuf v1.3.3-alpha.regen.1
