package handler

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/cosmos/cosmos-sdk/codec"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-storage/utils"
	gitopiaapp "github.com/gitopia/gitopia/v6/app"
	"github.com/gitopia/gitopia/v6/x/gitopia/types"
	offchaintypes "github.com/gitopia/gitopia/v6/x/offchain/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const MAX_UPLOAD_SIZE = 2 * 1024 * 1024 * 1024 // 2GB

type uploadAttachmentResponse struct {
	Sha  string `json:"sha"`
	Size int64  `json:"size"`
}

type signData struct {
	Action       string `json:"action"`
	RepositoryId string `json:"repositoryId"`
	TagName      string `json:"tagName"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	Sha256       string `json:"sha256"`
}

func ReleaseAttachmentExists(attachments []*types.Attachment, name string) (int, bool) {
	for i, v := range attachments {
		if v.Name == name {
			return i, true
		}
	}
	return 0, false
}

func UploadAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "POST" {
		defer r.Body.Close()

		r.Body = http.MaxBytesReader(w, r.Body, MAX_UPLOAD_SIZE)
		if err := r.ParseMultipartForm(MAX_UPLOAD_SIZE); err != nil {
			http.Error(w, "The uploaded file is too big. Please choose an file that's less than 2GB in size", http.StatusBadRequest)
			return
		}

		err := r.ParseMultipartForm(32 << 20)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Get signature from form data
		signature := r.MultipartForm.Value["signature"][0]
		if signature == "" {
			http.Error(w, "Signature is required", http.StatusBadRequest)
			return
		}

		// convert base64 string to bytes
		txBytes, err := base64.StdEncoding.DecodeString(signature)
		if err != nil {
			http.Error(w, "Error decoding base64 signature string: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Verify transaction and permissions
		encConf := gitopiaapp.MakeEncodingConfig()
		offchaintypes.RegisterInterfaces(encConf.InterfaceRegistry)
		offchaintypes.RegisterLegacyAminoCodec(encConf.Amino)

		verifier := offchaintypes.NewVerifier(encConf.TxConfig.SignModeHandler())
		txDecoder := encConf.TxConfig.TxDecoder()

		decodedTx, err := txDecoder(txBytes)
		if err != nil {
			http.Error(w, "Error decoding transaction: "+err.Error(), http.StatusBadRequest)
			return
		}

		msgs := decodedTx.GetMsgs()
		if len(msgs) != 1 || len(msgs[0].GetSigners()) != 1 {
			http.Error(w, "Invalid signature", http.StatusBadRequest)
			return
		}

		address := msgs[0].GetSigners()[0].String()

		// decode the byte message
		msg := msgs[0].(*offchaintypes.MsgSignData)

		var data signData
		err = json.Unmarshal(msg.Data, &data)
		if err != nil {
			http.Error(w, "Invalid sign data", http.StatusBadRequest)
			return
		}

		repoId, err := strconv.ParseUint(data.RepositoryId, 10, 64)
		if err != nil {
			http.Error(w, "Invalid repository ID", http.StatusBadRequest)
			return
		}

		havePushPermission, err := utils.HavePushPermission(repoId, address)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error checking push permission: %s", err.Error()), http.StatusInternalServerError)
			return
		}

		if !havePushPermission {
			http.Error(w, "User does not have push permission", http.StatusUnauthorized)
			return
		}

		// Verify signature
		err = verifier.Verify(decodedTx)
		if err != nil {
			http.Error(w, "Invalid signature", http.StatusBadRequest)
			return
		}

		file, handler, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()

		tmpFile, err := ioutil.TempFile(os.TempDir(), "attachment-")
		if err != nil {
			log.Printf("cannot create temporary file, %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer os.Remove(tmpFile.Name())

		sha := sha256.New()
		_, err = io.Copy(io.MultiWriter(sha, tmpFile), file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		attachmentDir := viper.GetString("ATTACHMENT_DIR")
		shaString := hex.EncodeToString(sha.Sum(nil))
		filePath := fmt.Sprintf("%s/%s", attachmentDir, shaString)

		// verify the size
		if handler.Size != data.Size {
			http.Error(w, "Invalid size", http.StatusBadRequest)
			return
		}

		// verify the sha256
		if shaString != data.Sha256 {
			http.Error(w, "Invalid sha256", http.StatusBadRequest)
			return
		}

		queryClient, err := gitopia.GetQueryClient(viper.GetString("GITOPIA_ADDR"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		userQuotaRes, err := queryClient.Gitopia.UserQuota(context.Background(), &types.QueryUserQuotaRequest{
			Address: address,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		storageParamsRes, err := queryClient.Storage.Params(context.Background(), &storagetypes.QueryParamsRequest{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var oldSize int64
		if data.Action == "new-release" {

		} else if data.Action == "update-release" {
			// check asset with same name already exists
			res, err := queryClient.Storage.RepositoryReleaseAsset(context.Background(), &storagetypes.QueryRepositoryReleaseAssetRequest{
				RepositoryId: repoId,
				Tag:          data.TagName,
				Name:         data.Name,
			})
			if err != nil && !strings.Contains(err.Error(), "release asset not found") {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			oldSize = int64(res.ReleaseAsset.Size_)
		}

		repoRes, err := queryClient.Gitopia.Repository(context.Background(), &types.QueryGetRepositoryRequest{
			Id: repoId,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Calculate storage delta
		storageDelta := handler.Size - oldSize

		costInfo, err := utils.CalculateStorageCost(uint64(userQuotaRes.UserQuota.StorageUsed), uint64(storageDelta), storageParamsRes.Params)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// If there is a storage charge, check if user has sufficient balance
		if !costInfo.StorageCharge.IsZero() {
			balanceRes, err := queryClient.Bank.Balance(context.Background(), &banktypes.QueryBalanceRequest{
				Address: repoRes.Repository.Owner.Id,
				Denom:   costInfo.StorageCharge.Denom,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if balanceRes.Balance.Amount.LT(costInfo.StorageCharge.Amount) {
				http.Error(w, "insufficient balance for storage charge", http.StatusUnauthorized)
				return
			}
		}

		// Lock the asset mutex before writing the file
		utils.LockAsset(shaString)
		defer utils.UnlockAsset(shaString)

		localFile, err := os.Create(filePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer localFile.Close()

		tmpFile.Seek(0, io.SeekStart)

		_, err = io.Copy(localFile, tmpFile)
		if err != nil {
			log.Printf("cannot copy from temp file to attachment dir, %s", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		w.Header().Set("Content-Type", "application/json")

		resp := uploadAttachmentResponse{
			Sha:  shaString,
			Size: handler.Size,
		}

		json.NewEncoder(w).Encode(resp)
		return
	}
}

func GetAttachmentHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "GET" {
		defer r.Body.Close()

		fileName := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]

		releaseURL := strings.TrimSuffix(r.URL.Path, "/"+fileName)
		blocks := strings.SplitN(releaseURL, "/", 5)

		if len(blocks) != 5 {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		address := blocks[2]
		repositoryName := blocks[3]
		tagName := blocks[4]

		grpcUrl := viper.GetString("GITOPIA_ADDR")
		grpcConn, err := grpc.Dial(grpcUrl,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(nil).GRPCCodec())),
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer grpcConn.Close()

		queryClient := types.NewQueryClient(grpcConn)

		res, err := queryClient.RepositoryRelease(context.Background(), &types.QueryGetRepositoryReleaseRequest{
			Id:             address,
			RepositoryName: repositoryName,
			TagName:        tagName,
		})
		if err != nil {
			log.Printf("cannot find release, %s", err.Error())
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		i, exists := ReleaseAttachmentExists(res.Release.Attachments, fileName)
		if !exists {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		utils.LockAsset(res.Release.Attachments[i].Sha)
		defer utils.UnlockAsset(res.Release.Attachments[i].Sha)

		err = utils.CacheReleaseAsset(res.Release.RepositoryId, res.Release.TagName, fileName, viper.GetString("ATTACHMENT_DIR"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		sha := res.Release.Attachments[i].Sha
		filePath := fmt.Sprintf("%s/%s", viper.GetString("ATTACHMENT_DIR"), sha)
		file, err := os.Open(filePath)
		if err != nil {
			log.Printf("attachment does not exist, %s", err.Error())
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}
		defer file.Close()

		_, err = io.Copy(w, file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
