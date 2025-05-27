package utils

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cosmos/cosmos-sdk/codec"
	gitopia "github.com/gitopia/gitopia/v6/app"
	"github.com/gitopia/gitopia/v6/x/gitopia/types"
	"github.com/gitopia/gitopia/v6/x/gitopia/utils"
	offchaintypes "github.com/gitopia/gitopia/v6/x/offchain/types"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DecodeBasic extracts token from given header using HTTP Bearer Auth.
// It returns empty string if value is not presented or not valid.
func DecodeBearer(header http.Header) (token string) {
	reqToken := header.Get("Authorization")
	splitToken := strings.Split(reqToken, "Bearer ")
	if len(splitToken) != 2 {
		return ""
	}

	return splitToken[1]
}

// DecodeBasic extracts username and password from given header using HTTP Basic Auth.
// It returns empty strings if values are not presented or not valid.
func DecodeBasic(header http.Header) (username, password string) {
	if len(header) == 0 {
		return "", ""
	}

	fields := strings.Fields(header.Get("Authorization"))
	if len(fields) != 2 || fields[0] != "Basic" {
		return "", ""
	}

	p, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", ""
	}

	creds := strings.SplitN(string(p), ":", 2)
	if len(creds) == 1 {
		return creds[0], ""
	}
	return creds[0], creds[1]
}

func ValidateBasicAuth(req *http.Request, username, password string) (bool, error) {
	repoId, err := ParseRepositoryIdfromURI(req.URL.Path)
	if err != nil {
		return false, err
	}

	encConf := gitopia.MakeEncodingConfig()
	offchaintypes.RegisterInterfaces(encConf.InterfaceRegistry)
	offchaintypes.RegisterLegacyAminoCodec(encConf.Amino)

	verifier := offchaintypes.NewVerifier(encConf.TxConfig.SignModeHandler())
	txDecoder := encConf.TxConfig.TxJSONDecoder()

	tx, err := txDecoder([]byte(password))
	if err != nil {
		return false, fmt.Errorf("error decoding")
	}

	// Verify signature
	err = verifier.Verify(tx)
	if err != nil {
		return false, err
	}

	// Verify push permission
	msgs := tx.GetMsgs()
	if len(msgs) != 1 || len(msgs[0].GetSigners()) != 1 {
		return false, fmt.Errorf("invalid signature")
	}

	address := msgs[0].GetSigners()[0].String()
	havePushPermission, err := HavePushPermission(repoId, address)
	if err != nil {
		return false, fmt.Errorf("error checking push permission: %s", err.Error())
	}

	if !havePushPermission {
		return false, fmt.Errorf("user does not have push permission")
	}

	return true, nil
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
		resp, err := queryClient.DaoMemberAll(context.Background(), &types.QueryAllDaoMemberRequest{
			DaoId: repo.Owner.Id,
		})
		if err != nil {
			return havePermission, err
		}
		for _, member := range resp.Members {
			if member.Member.Address == address {
				havePermission = true
				break
			}
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
