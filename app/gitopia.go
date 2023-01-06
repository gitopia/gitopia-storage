package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/authz"
	"github.com/gitopia/git-server/logger"
	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	rpcclient "github.com/tendermint/tendermint/rpc/client"
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	GITOPIA_ACC_ADDRESS_PREFIX = "gitopia"
	GAS_ADJUSTMENT             = 1.5
	MAX_TRIES                  = 5
	MAX_WAIT_BLOCKS            = 10
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
	rc  rpcclient.Client
	w   *io.PipeWriter
}

func NewGitopiaClient(ctx context.Context, cc client.Context, txf tx.Factory) (GitopiaClient, error) {
	w := logger.FromContext(ctx).WriterLevel(logrus.DebugLevel)
	cc = cc.WithOutput(w)

	txf = txf.WithFees("500utlore")

	grpcConn, err := grpc.Dial(viper.GetString("gitopia_grpc_url"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(nil).GRPCCodec())),
	)
	if err != nil {
		return GitopiaClient{}, errors.Wrap(err, "error creating grpc client")
	}

	qc := types.NewQueryClient(grpcConn)

	rc, err := rpchttp.New(cc.NodeURI, "/websocket")
	if err != nil {
		return GitopiaClient{}, errors.Wrap(err, "error creating rpc client")
	}

	return GitopiaClient{
		cc:  cc,
		txf: txf,
		qc:  qc,
		rc:  rc,
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
	txHash, err := BroadcastTx(g.cc, g.txf.WithSequence(0), &execMsg)
	if err != nil {
		return err
	}

	_, err = g.waitForTx(ctx, txHash)
	if err != nil {
		return errors.Wrap(err, "error waiting for tx")
	}

	return nil
}

// status returns the node status
func (g GitopiaClient) status(ctx context.Context) (*ctypes.ResultStatus, error) {
	return g.rc.Status(ctx)
}

// latestBlockHeight returns the lastest block height of the app.
func (g GitopiaClient) latestBlockHeight(ctx context.Context) (int64, error) {
	resp, err := g.status(ctx)
	if err != nil {
		return 0, err
	}
	return resp.SyncInfo.LatestBlockHeight, nil
}

// waitForNextBlock waits until next block is committed.
// It reads the current block height and then waits for another block to be
// committed, or returns an error if ctx is canceled.
func (g GitopiaClient) waitForNextBlock(ctx context.Context) error {
	return g.waitForNBlocks(ctx, 1)
}

// waitForNBlocks reads the current block height and then waits for anothers n
// blocks to be committed, or returns an error if ctx is canceled.
func (g GitopiaClient) waitForNBlocks(ctx context.Context, n int64) error {
	start, err := g.latestBlockHeight(ctx)
	if err != nil {
		return err
	}
	return g.waitForBlockHeight(ctx, start+n)
}

// waitForBlockHeight waits until block height h is committed, or returns an
// error if ctx is canceled.
func (g GitopiaClient) waitForBlockHeight(ctx context.Context, h int64) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for i := 0; i < MAX_TRIES; i++ {
		latestHeight, err := g.latestBlockHeight(ctx)
		if err != nil {
			return err
		}
		if latestHeight >= h {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx.Err(), "context is cancelled")
		case <-ticker.C:
		}
	}

	return fmt.Errorf("timeout error")
}

// waitForTx requests the tx from hash, if not found, waits for next block and
// tries again. Returns an error if ctx is canceled.
func (g GitopiaClient) waitForTx(ctx context.Context, hash string) (*ctypes.ResultTx, error) {
	bz, err := hex.DecodeString(hash)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to decode tx hash '%s'", hash)
	}
	for i := 0; i < MAX_WAIT_BLOCKS; i++ {
		resp, err := g.rc.Tx(ctx, bz, false)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				// Tx not found, wait for next block and try again
				err := g.waitForNextBlock(ctx)
				if err != nil {
					return nil, errors.Wrap(err, "waiting for next block")
				}
				continue
			}
			return nil, errors.Wrapf(err, "fetching tx '%s'", hash)
		}
		// Tx found
		return resp, nil
	}

	return nil, fmt.Errorf("max block wait exceeded")
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

func (g GitopiaClient) SetPullRequestState(ctx context.Context, creator string, repositoryId, iid uint64, state string, mergeCommitSha string, taskId uint64) error {
	msg := &types.MsgSetPullRequestState{
		Creator:        creator,
		RepositoryId:   repositoryId,
		Iid:            iid,
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

func (g GitopiaClient) PullRequest(ctx context.Context, repositoryId uint64, pullRequestIid uint64) (types.PullRequest, error) {
	repoResp, err := g.qc.Repository(ctx, &types.QueryGetRepositoryRequest{
		Id: repositoryId,
	})
	if err != nil {
		return types.PullRequest{}, errors.WithMessage(err, "query error")
	}

	prResp, err := g.qc.RepositoryPullRequest(ctx, &types.QueryGetRepositoryPullRequestRequest{
		Id:             repoResp.Repository.Owner.Id,
		RepositoryName: repoResp.Repository.Name,
		PullIid:        pullRequestIid,
	})
	if err != nil {
		return types.PullRequest{}, errors.WithMessage(err, "query error")
	}

	return *prResp.PullRequest, nil
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
