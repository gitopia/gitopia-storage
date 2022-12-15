package app

import (
	"context"
	"io"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	"github.com/gitopia/git-server/logger"
	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
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
	cc  client.Context
	txf tx.Factory
	qc  types.QueryClient
	w   *io.PipeWriter
}

func NewGitopiaClient(ctx context.Context, cc client.Context, txf tx.Factory) (GitopiaClient, error) {
	kr, err := keyring.New("git-server-events", "test", viper.GetString("keyring_dir"), cc.Input, cc.Codec, cc.KeyringOptions...)
	if err != nil {
		return GitopiaClient{}, errors.Wrap(err, "error initializing keyring")
	}
	w := logger.FromContext(ctx).WriterLevel(logrus.DebugLevel)
	cc = cc.WithKeyring(kr).WithOutput(w)

	txf = txf.WithGasPrices(viper.GetString("gas_prices"))

	grpcConn, err := grpc.Dial(viper.GetString("gitopia_grpc_url"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(nil).GRPCCodec())),
	)
	if err != nil {
		return GitopiaClient{}, errors.Wrap(err, "error creating grpc client")
	}

	qc := types.NewQueryClient(grpcConn)

	return GitopiaClient{
		cc:  cc,
		txf: txf,
		qc:  qc,
		w:   w,
	}, nil
}

// implement io.Closer
func (g GitopiaClient) Close() error {
	return g.w.Close()
}

func (g GitopiaClient) authorizedBroadcastTx(ctx context.Context, msg sdk.Msg) error {
	execMsg := authz.NewMsgExec(g.cc.FromAddress, []sdk.Msg{msg})
	// !!HACK!! set sequence to 0 to force refresh account sequence for every txn
	err := tx.BroadcastTx(g.cc, g.txf.WithSequence(0), &execMsg)
	if err != nil {
		return errors.Wrap(err, "error broadcasting tx")
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

	err := g.authorizedBroadcastTx(ctx, msg)
	if err != nil {
		return errors.WithMessage(err, "error sending authorized tx")
	}

	return nil
}

func (g GitopiaClient) ForkRepositorySuccess(ctx context.Context, creator string, repositoryId types.RepositoryId, taskId uint64) error {
	msg := &types.MsgForkRepositorySuccess{
		Creator:      creator,
		RepositoryId: repositoryId,
		TaskId:       taskId,
	}

	err := g.authorizedBroadcastTx(ctx, msg)
	if err != nil {
		return errors.WithMessage(err, "error sending authorized tx")
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

	err := g.authorizedBroadcastTx(ctx, msg)
	if err != nil {
		return errors.WithMessage(err, "error sending authorized tx")
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

	err := g.authorizedBroadcastTx(ctx, msg)
	if err != nil {
		return errors.WithMessage(err, "error sending authorized tx")
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
	resp, err := g.qc.CheckGitServerAuthorization(ctx, &types.QueryCheckGitServerAuthorizationRequest{
		UserAddress:     userAddress,
		ProviderAddress: g.cc.FromAddress.String(),
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
