package main

import (
  "log"
  "os"
  //"fmt"

  "github.com/sosedoff/gitkit"
)

// HookInfo contains information about branch, before and after revisions.
// tmpPath is a temporary directory with checked out git tree for the commit.
func receive(hook *gitkit.HookInfo, tmpPath string) error {
  // log.Println("Action:", hook.Action)
  // log.Println("Ref:", hook.Ref)
  // log.Println("Ref name:", hook.RefName)
  // log.Println("Old revision:", hook.OldRev)
  // log.Println("New revision:", hook.NewRev)

  // // Check if push is non fast-forward (force)
  // force, err := gitkit.IsForcePush(hook)
  // if err != nil {
  //   return err
  // }

  // // Reject force push
  // if force {
  //   return fmt.Errorf("non fast-forward pushed are not allowed")
  // }

  // // Check if branch is being deleted
  // if hook.Action == gitkit.BranchDeleteAction {
  //   fmt.Println("Deleting branch!")
  //   return nil
  // }

  // // Getting a commit message is built-in
  // message, err := gitkit.ReadCommitMessage(hook.NewRev)
  // if err != nil {
  //   return err
  // }
  // log.Println("Commit message:", message)

  return nil
}

func main() {
  os.Exit(1)
  return
  receiver := gitkit.Receiver{
    MasterOnly:  false,         // if set to true, only pushes to master branch will be allowed
    TmpDir:      "/tmp/gitkit", // directory for temporary git checkouts
    HandlerFunc: receive,       // your handler function
  }

  // Git hook data is provided via STDIN
  if err := receiver.Handle(os.Stdin); err != nil {
    log.Println("Error:", err)
    os.Exit(1) // terminating with non-zero status will cancel push
  }
  // os.Exit(0)
}