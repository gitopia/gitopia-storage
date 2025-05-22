package merkleproof

import (
	"bytes"
	"testing"

	"github.com/ipfs/boxo/files"
	merkletree "github.com/wealdtech/go-merkletree/v2"
)

func TestGenerateAndVerifyChunkProof(t *testing.T) {
	tests := []struct {
		name        string
		chunks      [][]byte
		chunkSize   int
		verifyIndex uint64
		wantErr     bool
	}{
		{
			name: "valid chunks",
			chunks: [][]byte{
				[]byte("chunk1"),
			},
			chunkSize:   1024,
			verifyIndex: 0,
			wantErr:     false,
		},
		{
			name:        "empty chunks",
			chunks:      [][]byte{},
			chunkSize:   1024,
			verifyIndex: 0,
			wantErr:     true,
		},
		{
			name: "single chunk",
			chunks: [][]byte{
				[]byte("chunk1"),
			},
			chunkSize:   1024,
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
			chunkSize:   1024,
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
			proof, root, chunkHash, err := GenerateChunkProof(file, tt.verifyIndex, tt.chunkSize)

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
			verified, err := VerifyPackfileProof(chunkHash, proof, root)
			if err != nil {
				t.Errorf("VerifyPackfileProof() error = %v", err)
				return
			}
			if !verified {
				t.Error("proof verification failed")
			}
		})
	}
}

func TestVerifyPackfileProof_Invalid(t *testing.T) {
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
			_, err := VerifyPackfileProof(tt.chunkHash, tt.proof, tt.root)
			if (err != nil) != tt.wantErr {
				t.Errorf("VerifyPackfileProof() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
