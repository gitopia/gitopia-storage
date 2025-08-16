package merkleproof

import (
	"bytes"
	"testing"

	"github.com/ipfs/boxo/files"
	merkletree "github.com/wealdtech/go-merkletree/v2"
)

func TestGenerateAndVerifyProof(t *testing.T) {
	tests := []struct {
		name        string
		chunks      [][]byte
		verifyIndex uint64
		wantErr     bool
	}{
		{
			name: "valid chunks",
			chunks: [][]byte{
				[]byte("chunk1"),
			},
			verifyIndex: 0,
			wantErr:     false,
		},
		{
			name:        "empty chunks",
			chunks:      [][]byte{},
			verifyIndex: 0,
			wantErr:     true,
		},
		{
			name: "single chunk",
			chunks: [][]byte{
				[]byte("chunk1"),
			},
			verifyIndex: 0,
			wantErr:     false,
		},
		{
			name: "large chunks",
			chunks: [][]byte{
				bytes.Repeat([]byte("a"), 1024),
				bytes.Repeat([]byte("b"), 1024),
				bytes.Repeat([]byte("c"), 1024),
				bytes.Repeat([]byte("d"), 1024),
			},
			verifyIndex: 2,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a file from chunks
			var fileContent bytes.Buffer
			for _, chunk := range tt.chunks {
				fileContent.Write(chunk)
			}
			file := files.NewBytesFile(fileContent.Bytes())

			// Generate proof for the specified chunk
			proof, root, chunkHash, err := GenerateProof(file, tt.verifyIndex)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("GenerateChunkProof() error = %v", err)
				return
			}

			if proof == nil {
				t.Error("expected proof, got nil")
				return
			}

			if root == nil {
				t.Error("expected root, got nil")
				return
			}

			if chunkHash == nil {
				t.Error("expected chunk hash, got nil")
				return
			}

			// Verify the proof
			verified, err := VerifyProof(chunkHash, proof, root)
			if err != nil {
				t.Errorf("VerifyProof() error = %v", err)
				return
			}
			if !verified {
				t.Error("proof verification failed")
			}
		})
	}
}

func TestVerifyProof_Invalid(t *testing.T) {
	tests := []struct {
		name      string
		chunkHash []byte
		proof     *merkletree.Proof
		root      []byte
		wantErr   bool
	}{
		{
			name:      "nil proof",
			chunkHash: []byte("chunk"),
			proof:     nil,
			root:      []byte("root"),
			wantErr:   true,
		},
		{
			name:      "empty root",
			chunkHash: []byte("chunk"),
			proof:     &merkletree.Proof{},
			root:      []byte{},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := VerifyProof(tt.chunkHash, tt.proof, tt.root)
			if (err != nil) != tt.wantErr {
				t.Errorf("VerifyProof() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
