package main

import (
	"github.com/gitopia/gitopia/x/gitopia/types"
)

func ReleaseAttachmentExists(attachments []*types.Attachment, name string) (int, bool) {
	for i, v := range attachments {
		if v.Name == name {
			return i, true
		}
	}
	return 0, false
}
