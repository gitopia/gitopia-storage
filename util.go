package main

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/gitopia/gitopia/x/gitopia/utils"
	gitopiautils "github.com/gitopia/gitopia/x/gitopia/utils"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func ReleaseAttachmentExists(attachments []*types.Attachment, name string) (int, bool) {
	for i, v := range attachments {
		if v.Name == name {
			return i, true
		}
	}
	return 0, false
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

func HavePushPermission(repoId uint64, address string) (bool, error) {
	var org types.Organization
	grpcUrl := viper.GetString("gitopia_grpc_url")
	grpcConn, err := grpc.Dial(grpcUrl,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
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

	if repo.Owner.Type == types.RepositoryOwner_ORGANIZATION {
		orgResp, err := queryClient.Organization(context.Background(), &types.QueryGetOrganizationRequest{
			Id: repo.Owner.Id,
		})
		if err != nil {
			return false, err
		}

		org = *orgResp.Organization
	}

	return gitopiautils.HavePermission(*repo, address, utils.PushBranchPermission, org), nil
}
