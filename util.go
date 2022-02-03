package main

import (
	"fmt"
	"net/http"

	"github.com/gitopia/gitopia/x/gitopia/types"
	"github.com/spf13/viper"
)

func mineArweaveTestnetBlock() error {
	arweaveGatewayUrl := viper.GetString("arweave_gateway_url")
	arweaveMineURL := fmt.Sprintf("%s/mine", arweaveGatewayUrl)
	client := &http.Client{}
	req, err := http.NewRequest("POST", arweaveMineURL, nil)
	req.Header.Add("X-Network", "arweave.testnet")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func ReleaseAttachmentExists(attachments []*types.Attachment, name string) (int, bool) {
	for i, v := range attachments {
		if v.Name == name {
			return i, true
		}
	}
	return 0, false
}
