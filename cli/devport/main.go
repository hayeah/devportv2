package main

import (
	"fmt"
	"os"

	devport "github.com/hayeah/devportv2"
)

func main() {
	managerIO := devport.ManagerIO{Stdout: os.Stdout, Stderr: os.Stderr}
	if err := Execute(managerIO); err != nil {
		fmt.Fprintln(managerIO.Stderr, err)
		os.Exit(1)
	}
}
