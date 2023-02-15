package utils

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/gitopia/gitopia/x/gitopia/utils"
	gogittransporthttp "github.com/gitopia/go-git/v5/plumbing/transport/http"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func GetCredential(req *http.Request) (gogittransporthttp.TokenAuth, error) {
	cred := gogittransporthttp.TokenAuth{}

	reqToken := req.Header.Get("Authorization")
	splitToken := strings.Split(reqToken, "Bearer ")
	if len(splitToken) != 2 {
		return cred, fmt.Errorf("authentication failed")
	}
	cred.Token = splitToken[1]

	return cred, nil
}

func ParseRepositoryIdfromURI(uri string) (uint64, error) {
	// u, err := url.Parse(uri)
	// if err != nil {
	// 	return 0, err
	// }

	s := strings.Split(uri, "/")
	if len(s) == 0 {
		return 0, errors.New("invalid uri")
	}

	idStr := strings.TrimSuffix(s[1], ".git")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return 0, err
	}

	return id, nil
}

func HavePushPermission(repoId uint64, address string) (havePermission bool, err error) {
	grpcUrl := viper.GetString("GITOPIA_ADDR")
	grpcConn, err := grpc.Dial(grpcUrl,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(nil).GRPCCodec())),
	)
	if err != nil {
		return false, err
	}
	defer grpcConn.Close()

	queryClient := types.NewQueryClient(grpcConn)

	repoResp, err := queryClient.Repository(context.Background(), &types.QueryGetRepositoryRequest{
		Id: repoId,
	})
	if err != nil {
		return false, err
	}

	repo := repoResp.Repository

	if repo.Owner.Type == types.OwnerType_USER {
		if address == repo.Owner.Id {
			havePermission = true
		}
	} else if repo.Owner.Type == types.OwnerType_DAO {
		member, err := queryClient.DaoMember(context.Background(), &types.QueryGetDaoMemberRequest{
			DaoId:  repo.Owner.Id,
			UserId: address,
		})
		if err != nil {
			return havePermission, err
		}
		if member.Member.Role == types.MemberRole_OWNER {
			havePermission = true
		}
	}

	if !havePermission {
		if i, exists := utils.RepositoryCollaboratorExists(repo.Collaborators, address); exists {
			if repo.Collaborators[i].Permission >= types.PushBranchPermission {
				havePermission = true
			}
		}
	}

	return havePermission, nil
}
