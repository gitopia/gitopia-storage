package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
)

func main() {
	repoPath := os.Args[1]
	files, err := ioutil.ReadDir(repoPath)
	if err != nil {
		log.Fatal(err)
	}

	for _, f := range files {
		if f.IsDir() {
			preReceiveHookf, err := os.OpenFile(repoPath+"/"+f.Name()+"/hooks/pre-receive",
				os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0777)
			if err != nil {
				fmt.Println("file open error:" + err.Error() + ":" + f.Name())
				continue
			}

			_, err = fmt.Fprintf(preReceiveHookf, "#!%s\n%s\n", "/bin/sh", "gitopia-pre-receive")
			if err != nil {
				fmt.Println("file write error:" + err.Error() + ":" + f.Name())
				continue
			}
		}
		fmt.Println("done:" + f.Name())
	}
}
