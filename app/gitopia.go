package app

import (
	"context"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	"github.com/gitopia/git-server/logger"
	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/ignite/cli/ignite/pkg/cosmosaccount"
	"github.com/ignite/cli/ignite/pkg/cosmosclient"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	GITOPIA_ACC_ADDRESS_PREFIX = "gitopia"
)

func InitGitopiaClientConfig() {
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount(
		GITOPIA_ACC_ADDRESS_PREFIX,
		GITOPIA_ACC_ADDRESS_PREFIX+sdk.PrefixPublic)
	//config.Seal()
}

type GitopiaClient struct {
	accountName string
	cc          cosmosclient.Client
	qc          types.QueryClient
}

func NewGitopiaClient(ctx context.Context, account string) (GitopiaClient, error) {
	client, err := cosmosclient.New(ctx,
		cosmosclient.WithNodeAddress(viper.GetString("tm_addr")),
		//cosmosclient.WithKeyringServiceName("cosmos"), // not suported on macos
		cosmosclient.WithKeyringBackend(cosmosaccount.KeyringTest),
		cosmosclient.WithHome(viper.GetString("keyring_dir")),
		cosmosclient.WithAddressPrefix(GITOPIA_ACC_ADDRESS_PREFIX),
	)
	if err != nil {
		return GitopiaClient{}, errors.Wrap(err, "error creating cosmos client")
	}

	grpcConn, err := grpc.Dial(viper.GetString("gitopia_grpc_url"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(nil).GRPCCodec())),
	)
	if err != nil {
		return GitopiaClient{}, errors.Wrap(err, "error creating grpc client")
	}

	queryClient := types.NewQueryClient(grpcConn)

	return GitopiaClient{
		accountName: account,
		cc:          client,
		qc:          queryClient,
	}, nil
}

func (g GitopiaClient) Address() (string, error) {
	addr, err := g.cc.Address(g.accountName)
	return addr, errors.Wrap(err, "error resolving address")
}

func (g GitopiaClient) broadcastTx(ctx context.Context, msg sdk.Msg) error {
	account, err := g.cc.Account(g.accountName)
	if err != nil {
		return errors.Wrap(err, "error retrieving the account")
	}

	txResp, err := g.cc.BroadcastTx(account, msg)
	if err != nil {
		return errors.Wrap(err, "error broadcasting tx")
	}
	if txResp.TxResponse.Code != 0 {
		logger.FromContext(ctx).Info("txResp: " + txResp.String())
		return errors.Wrap(err, "got error from broadcast tx")
	}
	return nil
}

func (g GitopiaClient) ForkRepository(ctx context.Context, creator string, repositoryId types.RepositoryId, owner string, taskId uint64) error {
	msg := &types.MsgForkRepository{
		Creator:      creator,
		RepositoryId: repositoryId,
		Owner:        owner,
		TaskId:       taskId,
	}

	address, err := g.Address()
	if err != nil {
		return errors.WithMessage(err, "address error")
	}

	accAddr, err := sdk.AccAddressFromBech32(address)
	if err != nil {
		return errors.WithMessage(err, "address conversion error")
	}

	execMsg := authz.NewMsgExec(accAddr, []sdk.Msg{msg})
	err = g.broadcastTx(ctx, &execMsg)
	if err != nil {
		return errors.WithMessage(err, "broadcast error")
	}
	return nil
}

func (g GitopiaClient) ForkRepositorySuccess(ctx context.Context, creator string, repositoryId types.RepositoryId, taskId uint64) error {
	msg := &types.MsgForkRepositorySuccess{
		Creator:      creator,
		RepositoryId: repositoryId,
		TaskId:       taskId,
	}

	address, err := g.Address()
	if err != nil {
		return errors.WithMessage(err, "address error")
	}

	accAddr, err := sdk.AccAddressFromBech32(address)
	if err != nil {
		return errors.WithMessage(err, "address conversion error")
	}

	execMsg := authz.NewMsgExec(accAddr, []sdk.Msg{msg})
	err = g.broadcastTx(ctx, &execMsg)
	if err != nil {
		return errors.WithMessage(err, "broadcast error")
	}
	return nil
}

func (g GitopiaClient) UpdateTask(ctx context.Context, creator string, id uint64, state types.TaskState, message string) error {
	msg := &types.MsgUpdateTask{
		Creator: creator,
		Id:      id,
		State:   state,
		Message: message,
	}

	address, err := g.Address()
	if err != nil {
		return errors.WithMessage(err, "address error")
	}

	accAddr, err := sdk.AccAddressFromBech32(address)
	if err != nil {
		return errors.WithMessage(err, "address conversion error")
	}

	execMsg := authz.NewMsgExec(accAddr, []sdk.Msg{msg})
	err = g.broadcastTx(ctx, &execMsg)
	if err != nil {
		return errors.WithMessage(err, "broadcast error")
	}
	return nil
}

func (g GitopiaClient) SetPullRequestState(ctx context.Context, creator string, id uint64, state string, mergeCommitSha string, taskId uint64) error {
	msg := &types.MsgSetPullRequestState{
		Creator:        creator,
		Id:             id,
		State:          state,
		MergeCommitSha: mergeCommitSha,
		TaskId:         taskId,
	}

	address, err := g.Address()
	if err != nil {
		return errors.WithMessage(err, "address error")
	}

	accAddr, err := sdk.AccAddressFromBech32(address)
	if err != nil {
		return errors.WithMessage(err, "address conversion error")
	}

	execMsg := authz.NewMsgExec(accAddr, []sdk.Msg{msg})
	err = g.broadcastTx(ctx, &execMsg)
	if err != nil {
		return errors.WithMessage(err, "broadcast error")
	}
	return nil
}

func (g GitopiaClient) RepositoryName(ctx context.Context, id uint64) (string, error) {
	resp, err := g.qc.Repository(ctx, &types.QueryGetRepositoryRequest{
		Id: id,
	})
	if err != nil {
		return "", errors.WithMessage(err, "query error")
	}

	return resp.Repository.Name, nil
}

func (g GitopiaClient) CheckGitServerAuthorization(ctx context.Context, userAddress string) (bool, error) {
	address, err := g.Address()
	if err != nil {
		return false, errors.WithMessage(err, "address error")
	}

	resp, err := g.qc.CheckGitServerAuthorization(ctx, &types.QueryCheckGitServerAuthorizationRequest{
		UserAddress:     userAddress,
		ProviderAddress: address,
	})
	if err != nil {
		return false, errors.WithMessage(err, "query error")
	}

	return resp.HaveAuthorization, nil
}

func (g GitopiaClient) RepositoryId(ctx context.Context, address string, repoName string) (uint64, error) {
	resp, err := g.qc.AnyRepository(ctx, &types.QueryGetAnyRepositoryRequest{
		Id:             address,
		RepositoryName: repoName,
	})
	if err != nil {
		return 0, errors.WithMessage(err, "query error")
	}

	return resp.Repository.Id, nil
}

func (g GitopiaClient) PullRequest(ctx context.Context, id uint64) (types.PullRequest, error) {
	resp, err := g.qc.PullRequest(ctx, &types.QueryGetPullRequestRequest{
		Id: id,
	})
	if err != nil {
		return types.PullRequest{}, errors.WithMessage(err, "query error")
	}

	return *resp.PullRequest, nil
}

func (g GitopiaClient) Task(ctx context.Context, id uint64) (types.Task, error) {
	res, err := g.qc.Task(ctx, &types.QueryGetTaskRequest{
		Id: id,
	})
	if err != nil {
		return types.Task{}, errors.Wrap(err, "query error")
	}

	return res.Task, nil
}
