package consumer

import (
	"fmt"
	"io"
	"os"

	"github.com/pkg/errors"
)

const (
	OFFSET_FILE = ".o"
	FILE_PERM   = 0666
)

type Client struct {
	f *os.File
}

func NewClient(topic string) (Client, error) {
	file, err := os.OpenFile("_"+topic+OFFSET_FILE, os.O_RDWR|os.O_CREATE, FILE_PERM)
	if err != nil {
		return Client{}, errors.Wrap(err, "error opening offset file")
	}
	return Client{
		f: file,
	}, nil
}

// write intensive use case. save all the offsets
func (c Client) Commit(offset uint64) error {
	err := c.f.Truncate(0)
	if err != nil {
		return errors.Wrap(err, "error truncating offset file")
	}
	_, err = c.f.Seek(0, 0)
	if err != nil {
		return errors.Wrap(err, "error overwriting offset file")
	}
	_, err = fmt.Fprintf(c.f, "%d", offset)
	if err != nil {
		return errors.Wrap(err, "error formatting offset file")
	}
	// ensure all data s written to disk. no data loss at the cost of latency and CPU resources
	err = c.f.Sync()
	if err != nil {
		return errors.Wrap(err, "error syncing offset file")
	}
	return nil
}

func (c Client) Offset() (uint64, error) {
	n := uint64(0)
	_, err := fmt.Fscanf(c.f, "%d", &n)
	if err != nil && err != io.EOF {
		return 0, errors.Wrap(err, "error reading offset file")
	}
	return n, nil
}
