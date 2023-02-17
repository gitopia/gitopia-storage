package main

import (
	"fmt"
	"os"

	"github.com/sosedoff/gitkit"
	//"fmt"
)

// HookInfo contains information about branch, before and after revisions.
// tmpPath is a temporary directory with checked out git tree for the commit.
func receive(hook *gitkit.HookInfo, tmpPath string) error {
  // Check if push is non fast-forward (force)
  force, err := gitkit.IsForcePush(hook)
  if err != nil {
    return err
  }

  // Reject force push
  if force {
    return fmt.Errorf("non fast-forward pushed are not allowed")
  }

  return nil
}

func main() {
	// f, err  := os.OpenFile("/tmp/gitkit/app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 777)
	// if err != nil {
	//   panic(err)
	// }
	// log.SetOutput(f)
	receiver := gitkit.Receiver{
		MasterOnly:  false,         // if set to true, only pushes to master branch will be allowed
		TmpDir:      "/tmp/gitkit", // directory for temporary git checkouts
		HandlerFunc: receive,       // your handler function
	}

	// Git hook data is provided via STDIN
	if err := receiver.Handle(os.Stdin); err != nil {
		// log.Println("Error:", err)
		msg := err.Error()
    // 4 length characters + 3 ng + 1 new line = 8
		//fmt.Fprintf(os.Stdout, "%04x%s\n", len(msg)+5, msg)
		fmt.Fprintf(os.Stdout, "%s\n",msg)
		os.Exit(1) // terminating with non-zero status will cancel push
	}
	// os.Exit(0)
}
